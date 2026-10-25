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

use crate::pkg::application::RelationshipWriteError;
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
pub(crate) mod tests {
    use std::collections::BTreeSet;
    use std::sync::{Mutex, PoisonError};

    use super::*;

    /// In-memory authorization graph: sorted relationships, cursor = the last
    /// returned relationship, optional outage.
    #[derive(Default)]
    pub(crate) struct FakeGraph {
        pub(crate) relationships: Mutex<BTreeSet<Relationship>>,
        pub(crate) writes: Mutex<Vec<(RelationshipOperation, String)>>,
        pub(crate) failure: Mutex<Option<String>>,
    }

    impl FakeGraph {
        pub(crate) fn with(relationships: impl IntoIterator<Item = Relationship>) -> Self {
            let graph = Self::default();
            graph
                .relationships
                .lock()
                .unwrap_or_else(PoisonError::into_inner)
                .extend(relationships);
            graph
        }

        pub(crate) fn contains(&self, relationship: &Relationship) -> bool {
            self.relationships
                .lock()
                .unwrap_or_else(PoisonError::into_inner)
                .contains(relationship)
        }

        pub(crate) fn writes(&self) -> Vec<(RelationshipOperation, String)> {
            self.writes
                .lock()
                .unwrap_or_else(PoisonError::into_inner)
                .clone()
        }

        fn failure(&self) -> Result<(), String> {
            self.failure
                .lock()
                .unwrap_or_else(PoisonError::into_inner)
                .clone()
                .map_or(Ok(()), Err)
        }
    }

    impl RelationshipGraph for FakeGraph {
        fn write(
            &self,
            operation: RelationshipOperation,
            relationship: &Relationship,
        ) -> Result<AuthorizationRevision, String> {
            self.failure()?;
            let mut relationships = self
                .relationships
                .lock()
                .unwrap_or_else(PoisonError::into_inner);
            match operation {
                RelationshipOperation::Touch => relationships.insert(relationship.clone()),
                RelationshipOperation::Delete => relationships.remove(relationship),
            };
            let mut writes = self.writes.lock().unwrap_or_else(PoisonError::into_inner);
            writes.push((operation, relationship.to_string()));
            Ok(AuthorizationRevision::new(format!("zed-{}", writes.len())))
        }

        fn read_page(
            &self,
            resource_type: &str,
            relation: &str,
            cursor: Option<&str>,
            limit: u32,
        ) -> Result<RelationshipPage, String> {
            self.failure()?;
            let limit = usize::try_from(limit).unwrap_or(usize::MAX);
            let relationships: Vec<Relationship> = self
                .relationships
                .lock()
                .unwrap_or_else(PoisonError::into_inner)
                .iter()
                .filter(|item| item.resource_type == resource_type && item.relation == relation)
                .filter(|item| cursor.is_none_or(|cursor| item.to_string().as_str() > cursor))
                .take(limit)
                .cloned()
                .collect();
            let cursor = relationships.last().map(ToString::to_string);
            Ok(RelationshipPage {
                relationships,
                cursor,
            })
        }
    }

    fn adapter(graph: &Arc<FakeGraph>) -> SpiceDbRelationships {
        SpiceDbRelationships::new(Arc::clone(graph) as Arc<dyn RelationshipGraph>)
    }

    #[test]
    fn register_courier_touches_courier_self() {
        let graph = Arc::new(FakeGraph::default());

        let revision = adapter(&graph)
            .register_courier(&CourierId::new("courier-1"))
            .expect("written");

        assert_eq!(revision, AuthorizationRevision::new("zed-1"));
        assert_eq!(
            graph.writes(),
            vec![(
                RelationshipOperation::Touch,
                "courier:courier-1#self@user:courier-1".to_owned()
            )]
        );
    }

    #[test]
    fn assignment_touches_and_unassignment_deletes_delivery_courier() {
        let graph = Arc::new(FakeGraph::default());
        let writer = adapter(&graph);
        let delivery = DeliveryId::new("delivery-1");
        let courier = CourierId::new("courier-1");

        let touched = writer.assign_courier(&delivery, &courier).expect("touch");
        assert!(graph.contains(&delivery_courier("delivery-1", "courier-1")));
        let deleted = writer
            .unassign_courier(&delivery, &courier)
            .expect("delete");

        assert_eq!(touched, AuthorizationRevision::new("zed-1"));
        assert_eq!(deleted, AuthorizationRevision::new("zed-2"));
        assert!(!graph.contains(&delivery_courier("delivery-1", "courier-1")));
        assert_eq!(
            graph.writes(),
            vec![
                (
                    RelationshipOperation::Touch,
                    "delivery:delivery-1#courier@user:courier-1".to_owned()
                ),
                (
                    RelationshipOperation::Delete,
                    "delivery:delivery-1#courier@user:courier-1".to_owned()
                ),
            ]
        );
    }

    #[test]
    fn graph_failure_is_a_write_error() {
        let graph = Arc::new(FakeGraph::default());
        *graph.failure.lock().unwrap_or_else(PoisonError::into_inner) =
            Some("spicedb timed out".to_owned());

        let error = adapter(&graph)
            .register_courier(&CourierId::new("courier-1"))
            .expect_err("fails closed");

        assert!(error.0.contains("spicedb timed out"), "{error}");
        assert!(
            error.0.contains("courier:courier-1#self@user:courier-1"),
            "{error}"
        );
    }
}
