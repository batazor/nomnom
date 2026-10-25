# 17. Cloud Native Application Runtime

Date: 2026-10-24

## Status

Accepted

Связан с [10. Использовать multi-stage container builds](0010-использовать-multi-stage-container-builds.md)

Связан с [12. Helm Charts per Deployable Application](0012-helm-charts-per-deployable-application.md)

Связан с [16. Запрещать обход internal gateway через Cilium и NetworkPolicy](0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md)

## Context

nomnom проектируется как cloud-native система: API, consumers, Temporal worker и BFF запускаются в контейнерах и управляются Kubernetes. Kubernetes перезапускает процессы, исключает их из балансировки, останавливает при rolling update, drain узла и scale-down. Если приложение не участвует в этом lifecycle, каждое обновление теряет запросы, consumers подтверждают необработанные сообщения или ждут `SIGKILL`, а probes, привязанные к внешним зависимостям, превращают частичную деградацию в полный отказ.

Уже приняты связанные решения: multi-stage образы с exec-form `ENTRYPOINT` и init для сигналов ([ADR 10](0010-использовать-multi-stage-container-builds.md)); Helm chart на каждое приложение с безопасными defaults и probes, которые включаются только после появления документированного health contract ([ADR 12](0012-helm-charts-per-deployable-application.md)); OpenTelemetry с lifecycle SDK в bootstrap процесса ([ADR 9](0009-использовать-opentelemetry-и-grafana-observability-stack.md)); composition root на каждый процесс ([ADR 5](0005-dependency-injection-strategy.md)); at-least-once delivery с Inbox/Outbox ([ADR 11](0011-transactional-inbox-and-idempotent-event-processing.md), [ADR 13](0013-transactional-outbox-for-integration-event-publishing.md)); синхронные бизнес-вызовы только через internal gateway ([ADR 15](0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md)), обход которого запрещён NetworkPolicy с ingress бизнес-порта `grpc` только от gateway и scrape Alloy только на порт `metrics` ([ADR 16](0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md)). Единого runtime contract, связывающего эти решения, пока нет, а приложения реализованы на четырёх языках.

На этом шаге фиксируется только архитектурное решение. Health endpoints, обработка сигналов, изменения Dockerfile и Helm charts не создаются.

## Decision

Принять Cloud Native Runtime Contract для всех запускаемых приложений. Подробные правила описаны в [документе cloud-native runtime](../cloud-native-runtime.md).

### Container lifecycle — часть runtime-дизайна

Каждое приложение самостоятельно реализует последовательность:

```text
start → validate configuration → initialize telemetry → initialize application
→ startup complete, ready → accept traffic/work
→ SIGTERM → not ready → readiness-propagation delay → stop accepting new work
→ drain in-flight work → stop workers → close resources → flush telemetry → exit
```

Kubernetes управляет lifecycle процесса; приложение корректно на него реагирует.

> Cloud-native awareness не означает внедрение Kubernetes SDK или Docker SDK в business/application code.

Lifecycle реализуется в composition root каждого процесса: configuration, telemetry, health, servers, consumers/workers и graceful shutdown. `domains` и `domains/<domain>/vo` не знают о containers, Kubernetes, signals, health probes и OpenTelemetry SDK. Application handlers получают cancellation и deadline через языковые абстракции (`context.Context`, `AbortSignal`, cancellation futures, отмена `asyncio`), а не Kubernetes-specific types. Background workers имеют owner, start, cancellation, graceful stop и bounded shutdown; неуправляемые goroutines, tasks и threads запрещены.

### Health: три разных состояния

- **Startup** — завершена собственная инициализация экземпляра.
- **Liveness** — процесс способен продолжать работу. Не зависит от Kafka, PostgreSQL, MongoDB, Temporal и соседних сервисов и остаётся успешной во время shutdown.
- **Readiness** — экземпляр может принимать новую работу: инициализация завершена, shutdown не идёт, worker способен принимать работу.

Внешняя зависимость включается в readiness только если без неё экземпляр не может выполнить ни одной своей функции; такое включение документируется. Readiness не транслирует отказ зависимости в полный отказ сервиса: по умолчанию отказ зависимости возвращается как ошибка конкретной операции. Health handler не вызывает зависимости синхронно. Для consumers и workers без входящего трафика readiness — диагностический сигнал.

HTTP-приложения предоставляют `/health/startup`, `/health/live`, `/health/ready`. Приложения без HTTP-стека реализуют `grpc.health.v1.Health` с именами `service`: `startup`, `liveness`, `readiness`; пустое имя отражает readiness. Ответы не содержат секретов, connection strings и деталей инфраструктуры.

### Технический порт отделён от бизнес-порта

Health и metrics обслуживаются на отдельном техническом порту, а не на бизнес-порту. Kubernetes-имя технического порта — `metrics`, как в модели политик ADR 16. NetworkPolicy и internal gateway разделяют технический и бизнес-трафик по портам: бизнес-порт `grpc` принимает трафик internal gateway, технический — kubelet probes и Alloy scrape. Технический порт слушает адрес pod, не публикуется через gateway или edge и не проходит бизнес-middleware. Health API не проходит через BFF или internal gateway.

### Graceful SIGTERM shutdown обязателен

Каждый процесс обрабатывает `SIGTERM`: помечает себя not ready, выжидает configurable readiness-propagation delay (не полагаясь на `preStop`), прекращает приём новой работы и отправляет `GOAWAY` для HTTP/2 и gRPC, завершает начатые операции в пределах deadline, останавливает workers и consumers, закрывает listeners и ресурсы, выполняет flush telemetry и завершается с exit `0` при штатном shutdown или non-zero при ошибке. `terminationGracePeriodSeconds` больше суммы delay, drain и flush.

Процесс в контейнере получает сигналы напрямую: exec-форма `ENTRYPOINT`/`CMD`, без shell-обёрток, с init-процессом там, где runtime не обрабатывает сигналы как PID 1 или порождает дочерние процессы.

Kafka consumers подтверждают только успешно обработанные сообщения и не подтверждают незавершённые ради быстрого shutdown. Повторная доставка после истечения deadline — штатное поведение at-least-once, корректность которого обеспечивают Inbox/Outbox и idempotency. Temporal worker `orders` прекращает получать задачи, а выполняющиеся Activities завершаются в пределах возможностей SDK; shutdown не меняет deterministic workflow logic, а lifecycle Temporal server не входит в lifecycle `orders`.

### Stateless и replaceable instances

Критичное состояние не хранится в `/tmp`, на локальном диске или в памяти процесса. Durable state находится в БД, Kafka, Temporal и других явно выбранных persistent systems; кеши и временные файлы допустимы, если их потеря не нарушает корректность. Сервисы с входящим трафиком работают не менее чем двумя репликами с `PodDisruptionBudget`; правило реализуется в charts отдельно.

### Конфигурация извне, fail fast

Конфигурация передаётся через environment variables, смонтированные файлы и secrets: `build once, run with different configuration`. Конфигурация валидируется при старте; невалидная приводит к понятной ошибке и non-zero exit без перехода в ready. Секреты не попадают в image, исходный код, `ConfigMap`, logs и traces.

### Учёт resource limits

Runtime учитывает cgroup limits там, где язык требует настройки: CPU и память для Go, heap и параллелизм для Node.js, модель процессов для Python, thread pools async runtime для Rust. Значения ресурсов не хардкодятся; приложение корректно работает при изменении requests и limits.

### Kubernetes metadata через Downward API

Pod name, namespace, node name и, при конкретной необходимости, labels и annotations передаются через Downward API в environment или файлы. Обычные сервисы не зависят от Kubernetes API, и ServiceAccount token по умолчанию не монтируется. Прямой Kubernetes client допустим только для приложений, чья ответственность — управление Kubernetes resources. Runtime metadata (service name, version, revision, build time, runtime version) используется в logs, resource attributes и диагностике и не входит в domain model.

### Observability providers участвуют в lifecycle

Telemetry инициализируется до приложения и завершается после остановки работы: `initialize telemetry → initialize application → mark ready` и `mark not ready → stop work → flush telemetry → shutdown providers → exit`. Ошибка telemetry backend не делает приложение unavailable и не блокирует shutdown дольше выделенного timeout.

### Правила для Helm

Probes используют реальные health interfaces на техническом порту, включая `startupProbe`; `terminationGracePeriodSeconds` соответствует shutdown contract; ServiceAccount token по умолчанию не монтируется; lifecycle hooks не заменяют graceful shutdown приложения, а `preStop` добавляется только позднее и под конкретную необходимость.

## Consequences

Rolling update, drain узла и scale-down становятся штатными операциями без потери принятых запросов и без подтверждения необработанных сообщений. Отказ внешней зависимости проявляется как деградация отдельных операций, а не как restart loop или одновременное исключение всех экземпляров. Одинаковая семантика health и shutdown во всех языках упрощает charts, эксплуатацию и диагностику, а бизнес-код остаётся переносимым и тестируемым без Kubernetes.

Каждый runtime требует собственной реализации обработки сигналов, drain HTTP/2 и gRPC, flush telemetry, учёта cgroup limits и корректного выхода Kafka consumer из группы; эти вопросы перечислены в документе cloud-native runtime и решаются при реализации. Readiness-propagation delay увеличивает время остановки pod. At-least-once повторы при shutdown остаются нормой и требуют соблюдения ADR 11 и ADR 13. Для BFF contract применяется в пределах возможностей Cosmo Router, а расхождения фиксируются как явные исключения.

Приложения и charts пока не соответствуют contract: health endpoints и обработка сигналов не реализованы, probes отключены, технический порт не объявлен, `startupProbe` и `terminationGracePeriodSeconds` в charts не настраиваются. Они приводятся к contract вместе с реализацией health endpoints. Номер технического порта, delay, timeouts и параметры probes определяются отдельно.
