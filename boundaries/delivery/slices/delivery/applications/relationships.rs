//! Relationship writer port of the `delivery` slice:
//! `delivery:<id>#courier@user:<courier_id>` mirrors `Delivery.courier_id`.
//! The SpiceDB adapter lives in `infrastructure/`.

use crate::pkg::application::RelationshipWriteError;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::DeliveryId;

/// Writes the relationships owned by the `delivery` slice.
///
/// Calls run synchronously inside the Unit of Work after the domain
/// transition succeeded and before commit; an error rolls the use case back
/// (fail closed). Use cases call the writer only when `Delivery.courier_id`
/// actually changes, so idempotent repeats write nothing.
pub trait DeliveryRelationshipWriter {
    /// Courier Assignment: `TOUCH delivery:<id>#courier@user:<courier_id>`.
    fn assign_courier(
        &self,
        delivery_id: &DeliveryId,
        courier_id: &CourierId,
    ) -> Result<(), RelationshipWriteError>;

    /// The assignment ended (Reassign Courier, Cancel Delivery):
    /// `DELETE delivery:<id>#courier@user:<courier_id>`.
    fn unassign_courier(
        &self,
        delivery_id: &DeliveryId,
        courier_id: &CourierId,
    ) -> Result<(), RelationshipWriteError>;
}
