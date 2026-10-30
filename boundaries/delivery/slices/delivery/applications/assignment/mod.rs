//! Release of a Courier Assignment, shared by every use case that ends one:
//! Reassign Courier (through `end_assignment`), Cancel
//! Delivery, Confirm Delivery and Report Delivery Failure. The caller runs
//! the delivery transition and saves the delivery in its Unit of Work.

pub mod advance_assignment;
pub mod create_delivery;
pub mod find_courier;
pub mod reassign_courier;

use crate::slices::courier::applications::assignment::CourierAssignment;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::Delivery;

use super::error::DeliveryApplicationError;
use super::relationships::DeliveryRelationshipWriter;

/// Makes the courier available again and revokes its access to the
/// delivery. `delivery#courier` follows the active assignment (FR-444): every
/// end of an assignment deletes `delivery:<id>#courier@user:<courier_id>`
/// before commit, including Confirm Delivery and Report Delivery Failure,
/// where `Delivery.courier_id` stays as the fact of who carried it. The
/// revision of the DELETE is stored with the delivery, so the next check sees
/// the revocation at once (AC-477, FR-447, AC-483). A failed delete fails the
/// release and rolls the Unit of Work back (fail closed, AC-692).
pub fn release_assignment(
    delivery: &mut Delivery,
    courier_id: &CourierId,
    couriers: &dyn CourierAssignment,
    relationships: &dyn DeliveryRelationshipWriter,
) -> Result<(), DeliveryApplicationError> {
    couriers.release(courier_id)?;
    let revision = relationships.unassign_courier(delivery.id(), courier_id)?;
    delivery.record_authorization_revision(revision);
    Ok(())
}
