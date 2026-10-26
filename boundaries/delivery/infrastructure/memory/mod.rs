//! Test doubles of the non-storage ports of `delivery`: a static
//! authorization, a recording relationship writer, a manual clock with
//! sequential ids and a static courier locator. Storage has no in-memory
//! variant: tests run against PostgreSQL (`infrastructure::repository::test_support`).

mod authorization;
mod clock;
mod locator;
mod relationships;

pub use authorization::StaticAuthorization;
pub use clock::{ManualClock, SequentialIdGenerator};
pub use locator::StaticCourierLocator;
pub use relationships::{RecordingRelationships, RelationshipWrite};
