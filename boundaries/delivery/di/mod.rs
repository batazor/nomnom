//! Composition roots процессов `delivery` (ADR 5): выбирают реализации
//! портов, собирают shaku-модули слайсов и итоговые модули процессов.
//!
//! - [`infrastructure`] — порты слайсов и их реализации ([`InfrastructureModule`]);
//! - [`service`] — граф use cases ([`ServiceModule`]) и тестовые графы [`build`];
//! - [`server`] — `cmd/delivery`: gRPC и reconciliation ([`server::ServerModule`]);
//! - [`relay`] — `cmd/delivery-relay`: Outbox → Kafka ([`relay::RelayModule`]);
//! - [`migrate`] — `cmd/delivery-migrate` ([`migrate::MigrateModule`]).
//!
//! Авторизация — SpiceDB (`infrastructure::spicedb`), хранилище —
//! PostgreSQL (`infrastructure::postgres`, ADR 0001 контекста), идентификаторы —
//! UUIDv7 (`infrastructure::ids`), locator — сохранённые позиции курьеров
//! (`infrastructure::locator`). Только тестовый граф [`build`] закрывается
//! in-memory адаптерами из `infrastructure::memory`.

mod http_worker;
pub mod infrastructure;
pub mod migrate;
mod reconciling_worker;
pub mod relay;
pub mod server;
pub mod service;

pub use http_worker::HttpWorker;
pub use infrastructure::{
    CourierStore, InfrastructureModule, ProcessAuthorization, ProcessRelationships, ProcessStores,
};
pub use reconciling_worker::ReconcilingWorker;
pub use server::{postgres, spicedb, spicedb_authorization};
pub use service::{ServiceModule, build, build_with_authorization};

#[cfg(test)]
pub(crate) use service::build_with_locator;
