//! Composition roots процессов `delivery` (ADR 5): выбирают реализации
//! портов, собирают shaku-модули слайсов и итоговые модули процессов.
//!
//! - [`infrastructure`] — порты слайсов и их реализации ([`InfrastructureModule`]);
//! - [`service`] — граф use cases ([`ServiceModule`]) и тестовые графы;
//! - [`server`] — `cmd/delivery`: gRPC, reconciliation, демо Simulator и
//!   consumer Delivery Duration Statistics ([`server::ServerModule`]); схему
//!   PostgreSQL процесс мигрирует сам при старте ([`server::store`]);
//! - [`worker`] — `cmd/delivery-worker`: Temporal worker Assignment Workflow
//!   ([`worker::WorkerProcess`]).
//!
//! Assignment Workflow use cases запускают и будят через Temporal
//! (`infrastructure::temporal`); тестовые графы записывают эти вызовы
//! (`infrastructure::memory::RecordingAssignment`).
//!
//! Авторизация — SpiceDB (`infrastructure::spicedb`), хранилище —
//! PostgreSQL (`infrastructure::repository`, ADR 0001 контекста), идентификаторы —
//! UUIDv7 (`infrastructure::ids`), locator — сохранённые позиции курьеров
//! (`infrastructure::locator`). Тестовые графы идут в тестовый PostgreSQL
//! (`infrastructure::repository::test_support`); статическая авторизация,
//! locator и запись relationships для них — `infrastructure::memory`.

mod consuming_worker;
mod http_worker;
pub mod infrastructure;
mod reconciling_worker;
pub mod server;
pub mod service;
mod simulator_worker;
pub mod worker;

pub use consuming_worker::ConsumingWorker;
pub use http_worker::HttpWorker;
pub use infrastructure::{
    AssignmentAdapters, CourierStore, InfrastructureModule, ProcessAssignment,
    ProcessAuthorization, ProcessRelationships, ProcessStores,
};
pub use reconciling_worker::ReconcilingWorker;
pub use server::{spicedb, spicedb_authorization, store};
pub use service::ServiceModule;
#[cfg(any(test, feature = "test-support"))]
pub use service::build_over;
pub use simulator_worker::{
    CourierActions, Randomness, Simulator, SimulatorSchedule, SimulatorWorker, SystemRandomness,
    UseCaseCourierActions,
};

#[cfg(test)]
pub(crate) use service::{build_over_store, build_with_locator};
