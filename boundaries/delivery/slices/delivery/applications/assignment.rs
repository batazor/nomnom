//! Release of a Courier Assignment, shared by every use case that ends one:
//! Reassign Courier and Replan Delivery (through `end_assignment`), Cancel
//! Delivery, Confirm Delivery and Report Delivery Failure. The caller runs
//! the delivery transition and saves the delivery in its Unit of Work.

use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::Delivery;

use super::error::DeliveryApplicationError;
use super::relationships::DeliveryRelationshipWriter;

/// Whether the release revokes `delivery:<id>#courier@user:<courier_id>`.
/// The relationship follows `Delivery.courier_id` (FR-444).
pub enum Revoke<'a> {
    /// The transition cleared `Delivery.courier_id` (Reassign, Replan,
    /// Cancel): the relationship is deleted before commit and the revision
    /// of the DELETE is stored with the delivery, so the next check sees the
    /// revocation at once (AC-477, FR-447, AC-483). A failed delete fails the
    /// release and rolls the Unit of Work back (fail closed).
    Relationship(&'a dyn DeliveryRelationshipWriter),
    /// The transition closed the delivery and kept `Delivery.courier_id`
    /// (Confirm Delivery, Report Delivery Failure): the relationship stays,
    /// and the closed state forbids any further courier action.
    Keep,
}

/// Makes the courier available again and, if asked, revokes its access to
/// the delivery.
pub fn release_assignment(
    delivery: &mut Delivery,
    courier_id: &CourierId,
    couriers: &dyn CourierAssignment,
    revoke: Revoke<'_>,
) -> Result<(), DeliveryApplicationError> {
    couriers.release(courier_id)?;
    if let Revoke::Relationship(relationships) = revoke {
        // Revoke access before commit; the revision of the DELETE makes the
        // revocation visible to the next check (FR-447, AC-483).
        let revision = relationships.unassign_courier(delivery.id(), courier_id)?;
        delivery.record_authorization_revision(revision);
    }
    Ok(())
}
