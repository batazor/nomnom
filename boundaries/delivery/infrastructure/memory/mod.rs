//! Test doubles of the non-storage ports of `delivery`: a static
//! authorization, a recording relationship writer, a recording Assignment
//! Workflow, a manual clock with
//! sequential ids, a static courier locator, a fixed random source and a
//! recording publisher of courier positions. Storage has no in-memory
//! variant: tests run against PostgreSQL (`infrastructure::repository::test_support`).
//!
//! Compiled only for tests: unit tests and, through the `test-support` feature,
//! the integration tests in `tests/`.

mod assignment;
mod authorization;
mod clock;
mod locator;
mod random;
mod relationships;
mod tracks;

pub use assignment::{AssignmentCall, RecordingAssignment};
pub use authorization::StaticAuthorization;
pub use clock::{ManualClock, SequentialIdGenerator};
pub use locator::StaticCourierLocator;
pub use random::FixedRandomSource;
pub use relationships::{RecordingRelationships, RelationshipWrite};
pub use tracks::RecordingPositionPublisher;
