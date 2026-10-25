//! Composition roots процессов `delivery` (ADR 5): выбирают реализации
//! портов, собирают shaku-модули слайсов и итоговые модули процессов.
//!
//! - [`infrastructure`] — порты слайсов и их реализации ([`InfrastructureModule`]);
//! - [`service`] — граф use cases ([`ServiceModule`]) и тестовые графы;
//! - [`server`] — `cmd/delivery`: gRPC, reconciliation, истечение Delivery
//!   Offers и Dispatch Deliveries ([`server::ServerModule`]); схему PostgreSQL
//!   процесс мигрирует сам при старте ([`server::postgres`]).
//!
//! Авторизация — SpiceDB (`infrastructure::spicedb`), хранилище —
//! PostgreSQL (`infrastructure::postgres`, ADR 0001 контекста), идентификаторы —
//! UUIDv7 (`infrastructure::ids`), locator — сохранённые позиции курьеров
//! (`infrastructure::locator`). Тестовые графы идут в тестовый PostgreSQL
//! (`infrastructure::postgres::test_support`); статическая авторизация,
//! locator и запись relationships для них — `infrastructure::memory`.

mod dispatch_worker;
mod http_worker;
pub mod infrastructure;
mod offer_expiry_worker;
mod reconciling_worker;
pub mod server;
pub mod service;

pub use dispatch_worker::{DispatchDeliveries, DispatchSchedule, DispatchWorker};
pub use http_worker::HttpWorker;
pub use infrastructure::{
    CourierStore, InfrastructureModule, ProcessAuthorization, ProcessRelationships, ProcessStores,
};
pub use offer_expiry_worker::{ExpireOffers, ExpirySchedule, OfferExpiryWorker};
pub use reconciling_worker::ReconcilingWorker;
pub use server::{postgres, spicedb, spicedb_authorization};
pub use service::{ServiceModule, build_over};

#[cfg(test)]
pub(crate) use service::{build_over_store, build_with_locator};
