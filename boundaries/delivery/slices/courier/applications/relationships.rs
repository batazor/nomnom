//! Relationship writer port of the `courier` slice. The authorization graph
//! is derived from business state: a registered `Courier` acts for itself.
//! The SpiceDB adapter lives in `infrastructure/`.

use crate::pkg::application::RelationshipWriteError;
use crate::pkg::persistence::AuthorizationRevision;
use crate::slices::courier::domains::courier::CourierId;

/// Writes the relationships owned by the `courier` slice.
///
/// Calls run synchronously before the business state is persisted; an error
/// aborts the use case (fail closed). A write that succeeded for a use case
/// that later failed is removed by reconciliation.
pub trait CourierRelationshipWriter {
    /// `TOUCH courier:<id>#self@user:<id>`: the courier is the user with the
    /// same id (`courier_id` = `user_id`, see `GLOSSARY.md`). Returns the
    /// revision (ZedToken) of the write, stored with the courier (FR-447).
    fn register_courier(
        &self,
        courier_id: &CourierId,
    ) -> Result<AuthorizationRevision, RelationshipWriteError>;
}
