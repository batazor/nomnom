# 15. Использовать internal gateway для синхронных вызовов бизнес-API

Date: 2026-10-24

## Status

Accepted

Дополняет [7. gRPC Contracts and Buf Schema Registry](0007-grpc-contracts-and-buf-schema-registry.md)

Связан с [16. Запрещать обход internal gateway через Cilium и NetworkPolicy](0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md)

## Context

[ADR 7](0007-grpc-contracts-and-buf-schema-registry.md) определил gRPC-контракты и BSR, но не
способ, которым вызывающее приложение находит backend. Если каждый caller знает Service
name, namespace и порт каждого API, топология кластера становится частью конфигурации всех
приложений, а транспортные политики (timeouts, retries, наблюдаемость) настраиваются в каждом
клиенте на разных языках по-разному. [ADR 12](0012-helm-charts-per-deployable-application.md)
уже требует, чтобы бизнес-вызовы не обращались к Kubernetes `Service` напрямую, но не выбирает
механизм.

## Decision

Все синхронные вызовы бизнес-API других приложений проходят через internal gateway:

| Взаимодействие | Маршрут |
| --- | --- |
| Клиентское приложение | Публичный API BFF |
| BFF вызывает bounded context | BFF → internal gateway → bounded context |
| Bounded context вызывает другой bounded context | Вызывающее приложение → internal gateway → целевое приложение |
| Temporal Activity вызывает бизнес-API другого контекста | Activity worker → internal gateway → bounded context |

BFF отвечает за клиентский API и агрегацию, gateway — за маршрутизацию и транспортные
политики. Бизнес-решения остаются в bounded contexts, оркестрация заказа — в Temporal.

Правило касается только синхронных вызовов бизнес-API. Инфраструктурные подключения
используют штатные endpoints: Kafka для событий ([ADR 4](0004-use-event-driven-architecture-with-apache-kafka.md)),
Temporal Server для SDK и workers, PostgreSQL и MongoDB для доступа владельца к своей БД,
Alloy для telemetry ([ADR 9](0009-использовать-opentelemetry-и-grafana-observability-stack.md)).

### Реализация

- [Kubernetes Gateway API](https://gateway-api.sigs.k8s.io/) — декларативные ресурсы;
  [Envoy Gateway](https://gateway.envoyproxy.io/) — controller; Envoy Proxy — data plane.
- Gateway `nomnom-gateway/internal` с listener `grpc` (HTTP/2 cleartext, порт 8080).
  Data plane доступен через Service `internal-gateway` типа `ClusterIP`; бизнес-маршруты
  наружу кластера не публикуются.
- Приложения получают адрес gateway только через конфигурацию `INTERNAL_GATEWAY_ADDR`.
  Адреса, порты и Service names backend'ов не являются настройками вызывающих приложений.
  Типизированные клиенты из Buf сохраняются: меняется только адрес подключения.
- Соответствие логического API Kubernetes Service хранится только в `GRPCRoute`.
  Маршруты централизованы в `nomnom-gateway`; listener принимает маршруты только из этого
  неймспейса, а backend-неймспейс разрешает ссылку на свой Service через `ReferenceGrant`.
- Маршрутизация выполняется по полному имени gRPC-сервиса и методу (`type: Exact`) из
  proto-контракта владельца. Неизвестный service или method не совпадает ни с одним правилом:
  Envoy отвечает HTTP 404, клиент получает `UNIMPLEMENTED`, backend вызов не получает.
- Gateway прозрачно передаёт gRPC metadata, `traceparent`, status codes и `grpc-timeout`;
  отмена клиента сбрасывает upstream stream. Встроенный request timeout Envoy Gateway (15 s)
  заменён явным верхним пределом `300s` в `BackendTrafficPolicy`, поэтому фактический
  deadline задаёт клиент.
- Retries задаются по классам методов, каждый класс — свои `GRPCRoute` с точным совпадением
  по методу и общая `BackendTrafficPolicy`; Gateway без политики не повторяет. Повторы — не
  более 2, без per-try timeout, в пределах deadline клиента; `cancelled` и `deadline-exceeded`
  не повторяются никогда.
  - Идемпотентные чтения (политика `reads`): `unavailable`, `connect-failure`,
    `refused-stream`, `reset-before-request`.
  - Команды (политика `commands`): только `connect-failure`, `refused-stream` и
    `reset-before-request` — отказы, при которых запрос гарантированно не дошёл до приложения
    (как transparent retries в [gRPC A6](https://github.com/grpc/proposal/blob/master/A6-client-retries.md)).
    `UNAVAILABLE` от приложения не повторяется: команда могла быть частично выполнена.
    Повтор после обработки — решение вызывающего с тем же idempotency key (activities Temporal).
- В реестр попадают только методы, у которых есть вызывающий; остальные получают
  `UNIMPLEMENTED` от gateway. Каждый новый метод добавляется с явным выбором класса.

## Consequences

Маршруты и транспортные политики настраиваются централизованно, в одном реестре и одним
механизмом для Go, TypeScript, Rust и Python. Перенос backend в другой namespace или Service
меняет только маршрут, а не конфигурацию вызывающих приложений. Gateway становится
естественной точкой для единых метрик, access logs и будущих политик аутентификации.

Каждый синхронный вызов получает дополнительный сетевой переход и задержку Envoy.
Gateway становится общей зависимостью всех синхронных взаимодействий: его недоступность
останавливает их, поэтому data plane запускается в нескольких репликах на разных узлах,
а ошибки маршрутизации влияют сразу на многих участников. Изменение маршрутов требует
ревью общего реестра.

Gateway не устраняет связанность: контрактная зависимость от proto-схем владельца и
синхронная временная связанность вызывающего и вызываемого приложения сохраняются.
mTLS и авторизация identity вызывающей стороны этим решением не определяются.
