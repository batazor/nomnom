//! Relationships owned by `delivery` (FR-444, design.md of change
//! `add-restaurant-delivery-relationships`) and their SpiceDB writer.
//!
//! | Relationship | Source of truth | Writes |
//! | --- | --- | --- |
//! | `courier:<id>#self@user:<id>` | registered `Courier` | `TOUCH` in Register Courier |
//! | `delivery:<id>#courier@user:<courier_id>` | `Delivery.courier_id` | `TOUCH` on Assignment, `DELETE` on Reassign Courier and Cancel Delivery |
//!
//! A courier is the user with the same id (`courier_id` = `user_id`).

use std::fmt::{Display, Formatter};
use std::sync::Arc;

use crate::pkg::authz::RelationshipWriteError;
use crate::pkg::persistence::AuthorizationRevision;
use crate::slices::courier::applications::relationships::CourierRelationshipWriter;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::applications::relationships::DeliveryRelationshipWriter;
use crate::slices::delivery::domains::delivery::DeliveryId;

pub const COURIER: &str = "courier";
pub const COURIER_SELF: &str = "self";
pub const DELIVERY: &str = "delivery";
pub const DELIVERY_COURIER: &str = "courier";
pub const USER: &str = "user";

/// One relationship `resource_type:resource_id#relation@subject_type:subject_id`.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub struct Relationship {
    pub resource_type: String,
    pub resource_id: String,
    pub relation: String,
    pub subject_type: String,
    pub subject_id: String,
}

impl Relationship {
    #[must_use]
    pub fn new(
        resource_type: &str,
        resource_id: &str,
        relation: &str,
        subject_type: &str,
        subject_id: &str,
    ) -> Self {
        Self {
            resource_type: resource_type.to_owned(),
            resource_id: resource_id.to_owned(),
            relation: relation.to_owned(),
            subject_type: subject_type.to_owned(),
            subject_id: subject_id.to_owned(),
        }
    }
}

impl Display for Relationship {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        write!(
            formatter,
            "{}:{}#{}@{}:{}",
            self.resource_type, self.resource_id, self.relation, self.subject_type, self.subject_id
        )
    }
}

/// `courier:<id>#self@user:<id>`.
#[must_use]
pub fn courier_self(courier_id: &str) -> Relationship {
    Relationship::new(COURIER, courier_id, COURIER_SELF, USER, courier_id)
}

/// `delivery:<id>#courier@user:<courier_id>`.
#[must_use]
pub fn delivery_courier(delivery_id: &str, courier_id: &str) -> Relationship {
    Relationship::new(DELIVERY, delivery_id, DELIVERY_COURIER, USER, courier_id)
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RelationshipOperation {
    /// Upsert: no error when the relationship already exists.
    Touch,
    /// No-op when the relationship does not exist.
    Delete,
}

/// One page of `ReadRelationships`; `cursor` resumes after its last result.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct RelationshipPage {
    pub relationships: Vec<Relationship>,
    pub cursor: Option<String>,
}

/// Synchronous access to the authorization graph; `Err` — the graph did not
/// answer (error, timeout or outage).
pub trait RelationshipGraph: Send + Sync {
    /// Applies one update and returns its revision (`written_at` ZedToken).
    fn write(
        &self,
        operation: RelationshipOperation,
        relationship: &Relationship,
    ) -> Result<AuthorizationRevision, String>;

    /// Fully consistent page of `resource_type:*#relation@*` after `cursor`.
    fn read_page(
        &self,
        resource_type: &str,
        relation: &str,
        cursor: Option<&str>,
        limit: u32,
    ) -> Result<RelationshipPage, String>;
}

/// SpiceDB adapter of the relationship writer ports of both slices.
pub struct SpiceDbRelationships {
    graph: Arc<dyn RelationshipGraph>,
}

impl std::fmt::Debug for SpiceDbRelationships {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("SpiceDbRelationships")
            .finish_non_exhaustive()
    }
}

impl SpiceDbRelationships {
    #[must_use]
    pub fn new(graph: Arc<dyn RelationshipGraph>) -> Self {
        Self { graph }
    }

    fn write(
        &self,
        operation: RelationshipOperation,
        relationship: &Relationship,
    ) -> Result<AuthorizationRevision, RelationshipWriteError> {
        self.graph.write(operation, relationship).map_err(|reason| {
            RelationshipWriteError(format!("{operation:?} {relationship}: {reason}"))
        })
    }
}

impl CourierRelationshipWriter for SpiceDbRelationships {
    fn register_courier(
        &self,
        courier_id: &CourierId,
    ) -> Result<AuthorizationRevision, RelationshipWriteError> {
        self.write(
            RelationshipOperation::Touch,
            &courier_self(courier_id.as_str()),
        )
    }
}

impl DeliveryRelationshipWriter for SpiceDbRelationships {
    fn assign_courier(
        &self,
        delivery_id: &DeliveryId,
        courier_id: &CourierId,
    ) -> Result<AuthorizationRevision, RelationshipWriteError> {
        self.write(
            RelationshipOperation::Touch,
            &delivery_courier(delivery_id.as_str(), courier_id.as_str()),
        )
    }

    fn unassign_courier(
        &self,
        delivery_id: &DeliveryId,
        courier_id: &CourierId,
    ) -> Result<AuthorizationRevision, RelationshipWriteError> {
        self.write(
            RelationshipOperation::Delete,
            &delivery_courier(delivery_id.as_str(), courier_id.as_str()),
        )
    }
}

#[cfg(test)]
pub(crate) mod tests;
