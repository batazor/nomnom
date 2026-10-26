# Control plane

Глобальный реестр топологии cell-based архитектуры ([ADR 41](../../docs/adr/0041-cell-based-architecture-with-control-plane.md)):
какие Cell есть и в какой Cell размещён каждый Market. Control plane не участвует в обработке
бизнес-запросов: ячейки к нему не обращаются, BFF кеширует Placement Map.

## Language

**Cell**:
Полный изолированный экземпляр data plane со своим internal gateway; `cell_id` — slug, адрес gateway — `host:port`. Неизменяема после регистрации.
_Avoid_: Cluster, region, shard

**Market**:
Город, в котором работает доставка; `market_id` — slug, название 1..64 символа, валюта ISO 4217. Создаётся сразу размещённым в Cell.
_Avoid_: Tenant, region, city id

**Market Placement**:
Размещение Market ровно в одной зарегистрированной Cell (`cell_id` Market).
_Avoid_: Partition, assignment

**Placement Map**:
Все Market Placement с адресами gateway их Cell — то, по чему BFF выбирает Cell запроса.
_Avoid_: Routing table, service discovery

**Idempotent Repeat**:
Повтор `RegisterCell`/`CreateMarket` с теми же значениями: успех с `created = false`; другие значения для известного id — `ALREADY_EXISTS`.
_Avoid_: Upsert, update
