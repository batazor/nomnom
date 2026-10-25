//! In-memory adapters of every `delivery` port. They serve application unit
//! tests and local wiring until the storage, SpiceDB and locator adapters are
//! chosen; they are not production implementations.

mod authorization;
mod clock;
mod locator;
mod store;

pub use authorization::StaticAuthorization;
pub use clock::{ManualClock, SequentialIdGenerator};
pub use locator::StaticCourierLocator;
pub use store::MemoryStore;
