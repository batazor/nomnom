# Cloud-native runtime

Этот документ задаёт runtime contract для всех запускаемых приложений nomnom: API, workers, consumers и BFF. Lifecycle контейнера является частью поведения приложения, а не только внешней Kubernetes-конфигурации: Kubernetes управляет процессом, а приложение корректно реагирует на его сигналы и сообщает своё состояние. Решение зафиксировано в [ADR 17](adr/0017-cloud-native-application-runtime.md).

Реализация в приложениях описана в разделе [Реализация](#реализация); обновление Helm charts выполняется следующим шагом. Открытые вопросы перечислены в [конце документа](#открытые-вопросы-по-runtime).

Cloud-native awareness не означает внедрение Kubernetes SDK или Docker SDK в business/application code. Приложение опирается на переносимые механизмы: сигналы процесса, HTTP/gRPC health, environment variables, смонтированные файлы и ограничения cgroup.

## Жизненный цикл процесса

```text
start
  → load and validate configuration
  → initialize telemetry
  → initialize application (composition root)
  → startup complete, ready
  → accept traffic / work
  → SIGTERM
  → not ready → readiness-propagation delay
  → stop accepting new work → drain in-flight work
  → stop workers and consumers → close listeners and resources
  → flush telemetry → shutdown providers
  → exit
```

Каждое приложение самостоятельно реализует эту последовательность в composition root. Бизнес-код не зависит от Kubernetes API, Docker API или конкретного container runtime: тот же процесс корректно работает локально, в Docker и в Kubernetes.

## Модель health

Приложение различает три состояния с разной семантикой и разными последствиями.

| Состояние | Вопрос | Последствие отказа в Kubernetes | Учитывает |
| --- | --- | --- | --- |
| Startup | Завершена ли начальная инициализация? | Пока не пройден, liveness и readiness не проверяются; превышение порога — restart. | Конфигурация загружена, telemetry и граф зависимостей собраны, listeners открыты. |
| Liveness | Способен ли процесс продолжать работу? | Restart контейнера. | Только внутреннее состояние процесса: работоспособность event loop, отсутствие необратимой внутренней ошибки. |
| Readiness | Может ли экземпляр сейчас принимать новую работу? | Pod остаётся запущенным, но исключается из endpoints `Service` и перестаёт получать новый трафик там, где это контролирует Kubernetes. | Инициализация завершена; не идёт shutdown; worker способен принимать работу. |

### Liveness

Liveness не зависит от доступности Kafka, PostgreSQL, MongoDB, Temporal, internal gateway или соседних сервисов. Временный отказ внешней зависимости не исправляется перезапуском процесса, поэтому не должен приводить к restart loop всех экземпляров.

Liveness остаётся успешной во время graceful shutdown: иначе kubelet может убить процесс до завершения drain.

### Readiness и внешние зависимости

Внешняя зависимость включается в readiness **только если без неё экземпляр не может выполнить ни одной своей функции**. Иначе readiness, зависящая от БД или Kafka, превращает частичную деградацию в полный отказ сервиса: все pods одновременно становятся not ready, а `Service` и internal gateway теряют все endpoints, включая те, что могли бы обслуживать не зависящие от этой зависимости операции.

Правила:

- по умолчанию readiness не включает внешние зависимости; отказ зависимости в конкретной операции возвращается вызывающему как ошибка этой операции, например gRPC `UNAVAILABLE` с `ErrorInfo.reason = DEPENDENCY_UNAVAILABLE` по [ADR 7](adr/0007-grpc-contracts-and-buf-schema-registry.md#ошибки);
- если зависимость всё же включена в readiness, это явное решение, записанное в документации приложения с обоснованием, что без неё не выполняется ни одна функция экземпляра;
- health handler не выполняет синхронные запросы к зависимостям: он читает уже вычисленное состояние, которое обновляется фоновым компонентом;
- изменение состояния зависимости в readiness использует порог или гистерезис, чтобы кратковременные ошибки не вызывали flapping.

### Consumers и workers

Для Kafka consumers, Outbox relay и Temporal workers Kubernetes не управляет входящей работой: они сами забирают её из Kafka, хранилища или Temporal. Для таких процессов readiness — диагностический сигнал: он показывает, что экземпляр инициализирован, не находится в shutdown и способен получать работу, но не влияет на распределение работы. Распределение определяется consumer group, Temporal task queue или механизмом распределения relay.

### Startup и зависимости

Startup означает завершение собственной инициализации экземпляра. Подключения к внешним зависимостям устанавливаются с ограниченными retries и backoff и не блокируют startup бесконечно; их влияние на readiness определяется правилом выше. Невалидная конфигурация не ожидает startup timeout, а немедленно завершает процесс ([Конфигурация](#конфигурация)).

## Технический порт и health endpoints

Health и metrics обслуживаются на **отдельном техническом порту**, а не на бизнес-порту. Разделение по портам позволяет NetworkPolicy и internal gateway различать технический и бизнес-трафик: бизнес-порт с именем `grpc` принимает трафик только от internal gateway, а технический порт — только от kubelet probes и Alloy scrape.

| Порт | Имя порта в Kubernetes | Назначение | Источники трафика |
| --- | --- | --- | --- |
| Бизнес | `grpc` (или `http` у текущих каркасов) | Бизнес-API | Internal gateway; для BFF — edge |
| Технический | `metrics` | `/health/*`, Prometheus-compatible `/metrics` при необходимости | Kubelet probes, Alloy scrape |

Имя `metrics` задано моделью политик [ADR 16](adr/0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md): политика `allow-metrics-scrape-from-alloy` разрешает ingress от Alloy только на порт с этим именем. Один технический порт обслуживает и health, и metrics, поэтому новых правил NetworkPolicy не требуется. Kubelet probes приходят с адреса узла и по ADR 16 разрешены CNI по умолчанию; это поведение проверяется тестом в kind.

Технический порт:

- слушает адрес pod, а не `localhost`: kubelet выполняет probes с адреса узла;
- не публикуется через internal gateway, BFF, edge или `GRPCRoute`/`HTTPRoute`; Health API не проходит через BFF или internal gateway;
- не содержит бизнес-операций и не требует аутентификации пользователя;
- не проходит через бизнес-middleware: аутентификацию, rate limiting, tracing каждого запроса и логирование каждого probe;
- номер порта задаётся конфигурацией; значение по умолчанию фиксируется при реализации и совпадает с `containerPort` в chart.

Метрики приложений по [ADR 9](adr/0009-использовать-opentelemetry-и-grafana-observability-stack.md) отправляются через OTLP в Alloy. Endpoint `/metrics` на техническом порту появляется только там, где runtime или библиотека отдаёт Prometheus-compatible метрики для scrape.

### HTTP

| Endpoint | Состояние | Успех | Отказ |
| --- | --- | --- | --- |
| `GET /health/startup` | Startup | `200` | `503` |
| `GET /health/live` | Liveness | `200` | `503` |
| `GET /health/ready` | Readiness | `200` | `503` |

Ответ определяется кодом статуса. Тело необязательно и, если присутствует, содержит минимальный JSON со статусом и логическими причинами отказа, например `initializing` или `shutting_down`. Health response не содержит секретов, connection strings, адресов, имён хостов, версий инфраструктуры и текстов ошибок зависимостей. Подробная диагностика пишется в logs и metrics.

### gRPC-only приложения

Приложение, которое не поднимает HTTP-стек, реализует на техническом порту стандартный [gRPC Health Checking Protocol](https://github.com/grpc/grpc/blob/master/doc/health-checking.md) (`grpc.health.v1.Health`). Kubernetes `grpc` probe передаёт поле `service`, поэтому три состояния отображаются на фиксированные имена:

| Состояние | `service` в probe и `HealthCheckRequest` | Статус успеха |
| --- | --- | --- |
| Startup | `startup` | `SERVING` |
| Liveness | `liveness` | `SERVING` |
| Readiness | `readiness` | `SERVING` |
| Общий статус сервера | `""` (пустая строка) | совпадает с `readiness` |

Пустое имя отражает readiness, потому что стандартные инструменты, например `grpc_health_probe` и клиентский health checking, по умолчанию запрашивают `""`. При shutdown `readiness` и `""` переводятся в `NOT_SERVING`, а `liveness` остаётся `SERVING`. Имена конкретных gRPC-сервисов в health service не регистрируются: health отражает состояние экземпляра, а не отдельных API.

Регистрация `grpc.health.v1.Health` на бизнес-порту этим документом не требуется. Если она понадобится, например для клиентского health checking, это отдельное решение с учётом NetworkPolicy.

## Graceful shutdown

Все процессы обрабатывают `SIGTERM`. `SIGINT` обрабатывается так же для локального запуска. Во всех четырёх runtime проекта действие `SIGTERM` по умолчанию — немедленное завершение без cleanup, поэтому явный обработчик обязателен.

Последовательность:

1. Пометить экземпляр как `not ready`. Liveness остаётся успешной.
2. Выждать configurable readiness-propagation delay. Удаление pod из EndpointSlice и обновление конфигурации internal gateway асинхронны: без задержки новые запросы, уже направленные на этот pod, получат connection refused. Приложение не зависит от `preStop` для этой задержки. Для процессов без входящего трафика delay может быть нулевым.
3. Прекратить принимать новую работу. Для HTTP/2 и gRPC отправить `GOAWAY`, чтобы долгоживущие соединения, в том числе от Envoy Proxy internal gateway, переподключились к другим экземплярам. Для HTTP/1.1 keep-alive соединения закрываются после текущего ответа.
4. Дать завершиться начатым операциям в пределах shutdown deadline.
5. Остановить background workers и consumers.
6. Закрыть listeners.
7. Закрыть соединения с БД и внешними системами.
8. Flush logs, traces, metrics и profiles, если SDK это поддерживает, с собственным ограниченным timeout.
9. Завершить процесс: exit `0` при штатном shutdown, non-zero при ошибке, включая принудительное прерывание работы по истечении deadline.

`SIGKILL` обработать невозможно. На него нельзя рассчитывать для cleanup: корректность после `SIGKILL`, OOM kill или падения узла обеспечивается не shutdown-кодом, а durable state, at-least-once доставкой, Inbox/Outbox и идемпотентностью.

Повторный сигнал во время shutdown не запускает последовательность заново. Допустимо ли ускорять завершение по повторному сигналу, решается для каждого runtime отдельно.

### Бюджет времени

Конкретные значения задаются позже. Логические параметры конфигурации:

| Параметр | Назначение |
| --- | --- |
| `shutdown.readiness_delay` | Ожидание распространения not ready (шаг 2). |
| `shutdown.drain_timeout` | Deadline на завершение начатой работы, остановку workers и закрытие ресурсов (шаги 3–7). |
| `shutdown.telemetry_flush_timeout` | Deadline на flush и shutdown telemetry providers (шаг 8). |

Имена environment variables повторяют путь ключа конфигурации в верхнем регистре с `__` между уровнями, без префикса проекта или сервиса: каждый процесс живёт в своём контейнере, например `SHUTDOWN__DRAIN_TIMEOUT`. Должно выполняться:

```text
terminationGracePeriodSeconds > readiness_delay + drain_timeout + telemetry_flush_timeout + запас
```

Если позднее появится `preStop`, его длительность входит в тот же `terminationGracePeriodSeconds`.

### Сигналы в контейнере

Процесс в контейнере должен реально получать сигналы. Требования к Dockerfile, дополняющие [ADR 10](adr/0010-использовать-multi-stage-container-builds.md):

- `ENTRYPOINT`/`CMD` только в exec-форме; shell-форма и обёртки `sh -c` не используются;
- entrypoint-скрипты, если появятся, завершаются `exec` приложения;
- если runtime приложения не устанавливает обработчик `SIGTERM` для PID 1 или процесс порождает дочерние процессы, используется минимальный init, пересылающий сигналы и собирающий zombie-процессы (сейчас `tini` в `restaurants`, `delivery`, `payments`);
- процесс, запущенный как PID 1 без init (сейчас `orders`), обязан сам установить обработчик `SIGTERM` и не порождать дочерних процессов.

## HTTP и gRPC серверы

При shutdown:

- новые запросы не принимаются, readiness остаётся `false` до завершения процесса;
- выполняющиеся запросы завершаются в пределах `drain_timeout`;
- клиентские deadlines и cancellation соблюдаются: истёкший deadline или отмена клиентом прекращают обработку;
- по истечении `drain_timeout` оставшиеся запросы отменяются через cancellation, а соединения закрываются.

Application code корректно обрабатывает cancellation context: не игнорирует отмену, не запускает новые побочные эффекты после неё и не продолжает работу без получателя результата. Длинные операции разбиваются на шаги, между которыми проверяется отмена.

## Kafka consumers

При shutdown consumer:

1. Прекращает получение новой работы.
2. Завершает обрабатываемые сообщения.
3. Подтверждает (commit offset / ack) только успешно обработанные сообщения, после commit локальной транзакции по [ADR 11](adr/0011-transactional-inbox-and-idempotent-event-processing.md).
4. Не подтверждает незавершённое сообщение ради быстрого shutdown.

Если shutdown deadline истёк, незакоммиченные сообщения будут доставлены повторно другому экземпляру после rebalance. Это штатное поведение at-least-once delivery, а не ошибка: корректность обеспечивают Transactional Inbox, Outbox и idempotency. Прерванная локальная транзакция откатывается целиком вместе с записью Inbox.

Outbox relay ([ADR 13](adr/0013-transactional-outbox-for-integration-event-publishing.md)) при shutdown прекращает выборку новых записей, дожидается подтверждения Kafka для уже отправленных и отмечает опубликованными только подтверждённые. Неотмеченные записи публикуются повторно другим экземпляром.

## Temporal workers

Для `orders`:

- при shutdown worker перестаёт забирать новые workflow и activity tasks из task queue;
- выполняющиеся Activities завершаются в пределах возможностей Temporal SDK и настроенного stop timeout; длительные Activities используют heartbeat, чтобы узнавать об отмене и чтобы Temporal быстрее переназначил незавершённую попытку;
- незавершённая Activity повторяется Temporal согласно её retry policy, поэтому Activities идемпотентны по [локальному ADR 2](../boundaries/orders/docs/adr/0002-use-temporal-to-orchestrate-orders.md);
- shutdown не меняет deterministic workflow logic: workflow-код не знает о сигналах процесса, а состояние workflow восстанавливается на другом worker из истории;
- sticky workflow cache является кешем и может быть потерян.

Lifecycle Temporal server не является частью lifecycle `orders`.

## Background workers

Любой background process (Outbox relay, периодическая задача, внутренний consumer, обновление кешей) имеет:

- явного owner в composition root;
- явный lifecycle start;
- cancellation mechanism;
- graceful stop, участвующий в последовательности shutdown;
- bounded shutdown: остановка завершается в пределах выделенного deadline.

Запрещены неуправляемые goroutines, tasks и threads, которые запускаются «в фоне» без владельца, отмены и ожидания завершения. Composition root, собирающий зависимости по [ADR 5](adr/0005-dependency-injection-strategy.md), отвечает и за их lifecycle.

## Конфигурация

Конфигурация передаётся извне:

- environment variables;
- смонтированные конфигурационные файлы при необходимости;
- смонтированные secrets.

Действует принцип `build once, run with different configuration`: один образ запускается во всех окружениях, а environment-specific значения не встраиваются в image.

Конфигурация валидируется при старте до открытия бизнес-listeners. Невалидная конфигурация приводит к fail fast: понятная ошибка с именем параметра без значения секрета, non-zero exit, без перехода в ready.

Секреты не попадают в Docker image, исходный код, `ConfigMap`, logs и traces. В Kubernetes они подключаются из внешних `Secret` по [ADR 12](adr/0012-helm-charts-per-deployable-application.md).

## Ресурсы контейнера

Runtime учитывает ограничения контейнера (cgroup) там, где язык требует настройки. Значения ресурсов не хардкодятся в коде или образе; приложение корректно работает при изменении requests и limits без пересборки.

| Runtime | CPU | Память | Требование |
| --- | --- | --- | --- |
| Go (`orders`) | Начиная с Go 1.25 `GOMAXPROCS` по умолчанию учитывает CPU limit cgroup; явное значение `GOMAXPROCS` отключает это поведение. | Go runtime не выводит `GOMEMLIMIT` из memory limit. | `GOMEMLIMIT` задаётся с запасом относительно memory limit: через environment из Downward API (`resourceFieldRef: limits.memory`) с учётом доли или вычислением при старте. Способ выбирается при реализации. |
| Node.js (`restaurants`) | `os.availableParallelism()` и размер libuv thread pool (`UV_THREADPOOL_SIZE`) выбираются осознанно; учёт CPU quota версией Node.js/libuv проверяется. | Размер V8 heap по умолчанию не должен считаться согласованным с memory limit без проверки. | `--max-old-space-size` или эквивалент задаётся с запасом относительно memory limit через конфигурацию. |
| Python (`payments`) | `os.cpu_count()` возвращает CPU узла и не учитывает quota. | Явного лимита heap нет. | Предпочтительна модель «один процесс на контейнер, масштабирование репликами». Если нужны несколько worker processes, их число задаётся конфигурацией или вычисляется из cgroup quota, а не из `os.cpu_count()`. |
| Rust (`delivery`) | `std::thread::available_parallelism()` учитывает cgroup quota на Linux; от него зависит число worker threads async runtime по умолчанию. | Нет GC; потребление определяется allocator и размерами буферов. | Размер worker pool и blocking pool async runtime ограничивается явно или проверяется по `available_parallelism`; очереди и буферы ограничены. |

Точные флаги, библиотеки и проверка поведения на целевых версиях runtime относятся к реализации.

## Stateless runtime

Приложения — заменяемые экземпляры. Критичное состояние, которое должно пережить restart, не хранится в `/tmp`, на локальном диске или в памяти процесса. Допустимы кеши, временные файлы и буферы, если их потеря не нарушает корректность.

Durable state хранится в явно выбранных persistent systems: PostgreSQL, MongoDB, Kafka, Temporal и других, выбранных отдельными решениями. Временные каталоги в Kubernetes подключаются как `emptyDir` с ограничением размера, совместимо с `readOnlyRootFilesystem`.

Следствие для Helm, фиксируемое как правило без реализации: сервисы с входящим трафиком работают как минимум с двумя репликами и получают `PodDisruptionBudget`, чтобы добровольные disruptions (drain узла, обновление кластера) не оставляли сервис без ready-экземпляров.

## Runtime metadata

Процесс сообщает о себе:

- service name;
- version;
- commit/revision;
- build time, если используется;
- runtime version.

Сведения используются в logs, OpenTelemetry resource attributes (`service.name`, `service.version`, `process.runtime.*` и атрибуты ревизии по актуальной версии semantic conventions) и диагностике. Они передаются при сборке (build args, linker flags, OCI labels) или через environment и не являются environment-specific конфигурацией. Runtime metadata не входит в domain model.

## Kubernetes metadata

Разрешено получать через [Downward API](https://kubernetes.io/docs/concepts/workloads/pods/downward-api/):

- pod name;
- namespace;
- node name, если нужен;
- labels и annotations — только при конкретном runtime use case.

Предпочтительны environment variables или файлы Downward API, например для `service.instance.id` и атрибутов `k8s.pod.name`, `k8s.namespace.name`. Прямой Kubernetes client допустим только для приложений, которым управление Kubernetes resources является частью ответственности. Обычным сервисам nomnom Kubernetes API не требуется, поэтому ServiceAccount token по умолчанию не монтируется.

## Observability lifecycle

```text
старт:     initialize telemetry → initialize application → mark ready
остановка: mark not ready → stop work → flush telemetry → shutdown providers → exit
```

Telemetry инициализируется первой, чтобы ошибки инициализации приложения попали в logs и traces, и завершается последней, чтобы сохранить сигналы shutdown. Ошибка или недоступность telemetry backend не делает приложение unavailable: не влияет на liveness и readiness и не блокирует shutdown дольше `telemetry_flush_timeout`. Это продолжает эксплуатационные правила [документа observability](observability.md#эксплуатационные-правила).

## Правила для Helm

Правила фиксируются сейчас, а charts изменяются отдельным шагом:

- probes используют реальные health interfaces на техническом порту: `startupProbe` → `/health/startup`, `livenessProbe` → `/health/live`, `readinessProbe` → `/health/ready` или `grpc` probe с `service` по таблице выше (`grpc` probe принимает только числовой порт, а не имя);
- probes включаются в chart после реализации health contract в приложении; временные endpoints на бизнес-порту health interface не заменяют;
- `terminationGracePeriodSeconds` соответствует бюджету shutdown приложения;
- ServiceAccount token по умолчанию не монтируется;
- lifecycle hooks не заменяют graceful shutdown приложения; `preStop` — опционально, позднее и под конкретную необходимость;
- сервисы с входящим трафиком получают `PodDisruptionBudget` и не менее двух реплик в окружениях, где важна доступность;
- каждый container и initContainer задаёт `resources.requests.memory` и `resources.limits.memory`, образ закреплён digest или tag кроме `latest`, pod template несёт `app.kubernetes.io/name`. Эти правила отклоняет при admission ValidatingAdmissionPolicy [`nomnom-workload-baseline`](../ops/kind/manifests/admission/workload-baseline.yaml) ([ADR 17](adr/0017-cloud-native-application-runtime.md)).

## Ответственность слоёв

Runtime lifecycle — техническая ответственность composition root каждого запускаемого процесса:

```text
composition root
  ├── configuration
  ├── telemetry
  ├── health
  ├── servers
  ├── consumers/workers
  └── graceful shutdown
```

Он находится вне `domains`. `domains` и `domains/<domain>/vo` не знают о containers, Kubernetes, signals, health probes и OpenTelemetry SDK. Application handlers получают cancellation и deadline через языковые абстракции, а не Kubernetes-specific types:

| Язык | Абстракция |
| --- | --- |
| Go | `context.Context` |
| TypeScript | `AbortSignal` |
| Rust | отмена future и cancellation token |
| Python | отмена `asyncio` task и timeout/deadline |

## Реализация

Каждый backend-процесс реализует contract собственным lifecycle-модулем в своём языке; общего кода между языками нет. Модули имеют одинаковую форму: composition root передаёт конфигурацию lifecycle и `Worker` — единицу работы с операциями start (захват ресурсов, например listener), run (обслуживание до shutdown) и shutdown с deadline. Lifecycle владеет техническим портом, health state, обработкой сигналов, readiness delay и drain и возвращает успех только при штатном shutdown.

| Приложение | Модуль | Технический порт по умолчанию |
| --- | --- | --- |
| `orders` | `pkg/lifecycle` (`lifecycle.New(cfg).Run(ctx, worker)`) | `9081` |
| `restaurants` | `pkg/lifecycle/lifecycle.ts` (`new Lifecycle(cfg).run(worker)`) | `9082` |
| `delivery` | `pkg/lifecycle` (`Lifecycle::bind(settings).await?.run(worker)`), `di::HttpWorker` | `9083` |
| `payments` | `pkg/lifecycle` (`Lifecycle(config).run(worker)`), `HttpWorker` | `9084` |

Общие параметры: `management.port` (`MANAGEMENT__PORT`, отличается от бизнес-порта), `shutdown.readiness_delay` (`SHUTDOWN__READINESS_DELAY`, по умолчанию `5s`, допускается `0`) и `shutdown.drain_timeout` (`SHUTDOWN__DRAIN_TIMEOUT`, по умолчанию `20s`). Длительности в environment записываются как `500ms`, `5s`, `1m`. `shutdown.telemetry_flush_timeout` появится вместе с OpenTelemetry SDK. Повторный сигнал во время shutdown завершает процесс сразу: в Go, Node.js и Python восстанавливается обработка по умолчанию, а в Rust, где `tokio` не умеет её вернуть, lifecycle прерывает drain и выходит с non-zero exit.

Один набор black-box сценариев проверяет contract на реальных процессах всех четырёх приложений: [`ops/runtime-contract`](../ops/runtime-contract/README.md), запуск — `make runtime-contract`. Завершение начатых запросов в пределах deadline и non-zero exit при его превышении покрыты unit-тестами lifecycle-модулей.

## Текущее состояние

Charts `orders`, `restaurants`, `delivery`, `payments` и `controlplane` соответствуют contract: технический порт `metrics`, `startupProbe`/`livenessProbe`/`readinessProbe` на `/health/*`, передача `MANAGEMENT__PORT` и `SHUTDOWN__*` в контейнер и `terminationGracePeriodSeconds`, который chart проверяет против суммы readiness delay и drain timeout. Модель NetworkPolicy из ADR 16 уже соответствует contract.

BFF использует сторонний Cosmo Router, который сам управляет health endpoints, shutdown и процессом router plugin. Для BFF contract применяется в пределах конфигурации Cosmo Router; невозможность вынести health на технический порт или иное расхождение фиксируется как явное исключение в документации BFF.

## Открытые вопросы по runtime

Решаются отдельно для каждого приложения при реализации:

| Вопрос | Go (`orders`) | TypeScript (`restaurants`) | Rust (`delivery`) | Python (`payments`) |
| --- | --- | --- | --- | --- |
| Сигналы и PID 1 | `signal.NotifyContext` в процессе PID 1 без init в distroless; подтвердить отсутствие дочерних процессов. | `process.on('SIGTERM')` под `tini`; исключить завершение через `process.exit` до drain. | `tokio::signal::unix` под `tini`. | Явный обработчик (`loop.add_signal_handler` или `signal.signal`) под `tini`; поведение выбранного ASGI/gRPC server при сигнале. |
| cgroup-aware limits | Способ задания `GOMEMLIMIT`; контроль, что `GOMAXPROCS` не переопределён. | Heap limit и учёт CPU quota в Node.js 24/libuv. | Размеры worker и blocking pools async runtime. | Модель процессов; число workers без `os.cpu_count()`. |
| GOAWAY и drain в gRPC-стеке | `grpc-go` `GracefulStop` с deadline и fallback на `Stop`. | Выбранный gRPC/Connect server для Node.js: GOAWAY и закрытие HTTP/2 sessions. | `tonic` `serve_with_shutdown`: GOAWAY и ожидание in-flight запросов. | `grpcio` `server.stop(grace)` или асинхронный аналог. |
| Flush телеметрии | `Shutdown` providers OTel и остановка Pyroscope с timeout. | `sdk.shutdown()` OTel и Pyroscope agent. | Shutdown OTel providers и Pyroscope agent. | `shutdown()` providers OTel и Pyroscope; совместимость с Python 3.14. |
| Kafka client и rebalance при shutdown | `watermill-kafka`: закрытие router/subscriber с timeout, commit offsets только после обработки, выход из consumer group. | `@confluentinc/kafka-javascript` (`pkg/infrastructure/kafka/consumer.ts`): offset сохраняется только после успешной обработки, `shutdown` дожидается начатой обработки до deadline, `disconnect` коммитит сохранённые offsets и выводит из consumer group. | Kafka consumers нет (Outbox доставляет Debezium, ADR 37); при появлении — те же требования. | Kafka consumers нет (Outbox доставляет Debezium, ADR 37); при появлении — те же требования. |

Общие для Kafka consumers вопросы: commit обработанных offsets при отзыве partitions во время rebalance; cooperative rebalancing; static membership (`group.instance.id` из pod name) как компромисс между быстрым rolling restart и задержкой переназначения partitions при scale-down.

Для Temporal worker `orders`: stop timeout worker, heartbeat timeout длительных Activities и их согласование с `drain_timeout`.

Также остаются открытыми: значения `telemetry_flush_timeout` и параметров probes; проверка, поддерживает ли Cosmo Router health на отдельном порту, его shutdown delay и init для дочернего процесса plugin.
