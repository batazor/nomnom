//! Преобразования запросов и ответов `PermissionsService` в protobuf и обратно.

use super::super::mapping::{CheckConsistency, PermissionCheck};
use super::super::relationships::{Relationship, RelationshipOperation};
use super::super::v1::check_permission_response::Permissionship;
use super::super::v1::consistency::Requirement;
use super::super::v1::relationship_update::Operation;
use super::super::v1::{
    CheckPermissionRequest, CheckPermissionResponse, Consistency, Cursor, ObjectReference,
    ReadRelationshipsRequest, RelationshipFilter, RelationshipUpdate, SubjectReference,
    WriteRelationshipsRequest, ZedToken,
};

pub(super) fn write_request(
    operation: RelationshipOperation,
    relationship: &Relationship,
) -> WriteRelationshipsRequest {
    let operation = match operation {
        RelationshipOperation::Touch => Operation::Touch,
        RelationshipOperation::Delete => Operation::Delete,
    };
    WriteRelationshipsRequest {
        updates: vec![RelationshipUpdate {
            operation: operation as i32,
            relationship: Some(to_proto(relationship)),
        }],
        optional_preconditions: Vec::new(),
        optional_transaction_metadata: None,
    }
}

/// Reconciliation сравнивает граф с хранилищем, поэтому читает
/// `fully_consistent`, а не кэш `minimize_latency`.
pub(super) fn read_request(
    resource_type: &str,
    relation: &str,
    cursor: Option<&str>,
    limit: u32,
) -> ReadRelationshipsRequest {
    ReadRelationshipsRequest {
        consistency: Some(Consistency {
            requirement: Some(Requirement::FullyConsistent(true)),
        }),
        relationship_filter: Some(RelationshipFilter {
            resource_type: resource_type.to_owned(),
            optional_relation: relation.to_owned(),
            ..RelationshipFilter::default()
        }),
        optional_limit: limit,
        optional_cursor: cursor.map(|token| Cursor {
            token: token.to_owned(),
        }),
    }
}

pub(super) fn to_proto(relationship: &Relationship) -> super::super::v1::Relationship {
    super::super::v1::Relationship {
        resource: Some(ObjectReference {
            object_type: relationship.resource_type.clone(),
            object_id: relationship.resource_id.clone(),
        }),
        relation: relationship.relation.clone(),
        subject: Some(SubjectReference {
            object: Some(ObjectReference {
                object_type: relationship.subject_type.clone(),
                object_id: relationship.subject_id.clone(),
            }),
            optional_relation: String::new(),
        }),
        optional_caveat: None,
        optional_expires_at: None,
    }
}

/// `None` для relationship без resource или subject.
pub(super) fn from_proto(relationship: &super::super::v1::Relationship) -> Option<Relationship> {
    let resource = relationship.resource.as_ref()?;
    let subject = relationship.subject.as_ref()?.object.as_ref()?;
    Some(Relationship {
        resource_type: resource.object_type.clone(),
        resource_id: resource.object_id.clone(),
        relation: relationship.relation.clone(),
        subject_type: subject.object_type.clone(),
        subject_id: subject.object_id.clone(),
    })
}

/// Только `HAS_PERMISSION` разрешает; `NO_PERMISSION`, conditional и
/// неизвестные значения — отказ.
pub(super) fn allowed(response: &CheckPermissionResponse) -> bool {
    response.permissionship == Permissionship::HasPermission as i32
}

pub(super) fn request(check: &PermissionCheck) -> CheckPermissionRequest {
    let requirement = match &check.consistency {
        CheckConsistency::MinimizeLatency => Requirement::MinimizeLatency(true),
        CheckConsistency::AtLeastAsFresh(revision) => Requirement::AtLeastAsFresh(ZedToken {
            token: revision.as_str().to_owned(),
        }),
    };
    CheckPermissionRequest {
        consistency: Some(Consistency {
            requirement: Some(requirement),
        }),
        resource: Some(ObjectReference {
            object_type: check.resource_type.to_owned(),
            object_id: check.resource_id.clone(),
        }),
        permission: check.permission.to_owned(),
        subject: Some(SubjectReference {
            object: Some(ObjectReference {
                object_type: check.subject_type.to_owned(),
                object_id: check.subject_id.clone(),
            }),
            optional_relation: String::new(),
        }),
        context: None,
        with_tracing: false,
    }
}
