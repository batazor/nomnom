# Control plane

Глобальный реестр топологии cell-based архитектуры ([ADR 41](../../docs/adr/0041-cell-based-architecture-with-control-plane.md)):
какие Cell есть и в какой Cell размещён каждый Market. Control plane не участвует в обработке
бизнес-запросов: из реестра он пишет маршруты публичного gateway, а ни gateway, ни ячейки к нему не
обращаются.

## Language

**Cell**:
Полный изолированный экземпляр data plane со своим Cosmo Router, BFF и internal gateway; `cell_id` — slug. Неизменяема после регистрации.
_Avoid_: Cluster, region, shard

**Market**:
Город, в котором работает доставка; `market_id` — slug, название 1..64 символа, валюта ISO 4217, часовой пояс IANA (`Europe/Berlin`). Создаётся сразу размещённым в Cell.
_Avoid_: Tenant, region, city id

**Market Placement**:
Размещение Market ровно в одной зарегистрированной Cell (`cell_id` Market).
_Avoid_: Partition, assignment

**Router Cell**:
Kubernetes Service Cosmo Router Cell (`namespace`, `name`, `port`) — точка входа Cell для клиентов, куда публичный gateway ведёт её Market.
_Avoid_: Gateway address, endpoint

**Маршрут Market**:
`HTTPRoute market-<market_id>` публичного gateway: `/m/<market_id>` в Router Cell его Market Placement. Пишет только control plane.
_Avoid_: Placement Map, routing table

**Список Market**:
JSON `GET /markets`, который Envoy публичного gateway отдаёт сам из `ConfigMap markets`; его строит control plane.
_Avoid_: Markets query

**Idempotent Repeat**:
Повтор `RegisterCell`/`CreateMarket` с теми же значениями: успех с `created = false`; другие значения для известного id — `ALREADY_EXISTS`.
_Avoid_: Upsert, update
