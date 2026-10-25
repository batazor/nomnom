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
