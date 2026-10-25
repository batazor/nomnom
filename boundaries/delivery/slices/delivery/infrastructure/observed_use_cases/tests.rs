use super::*;
use crate::pkg::application::UnitOfWorkError;
use crate::slices::delivery::domains::delivery::DeliveryId;

#[test]
fn expected_refusals_are_rejected_and_failures_are_errors() {
    let cases = [
        (
            DeliveryApplicationError::PermissionDenied,
            Failure::Rejected("permission_denied"),
        ),
        (
            DeliveryApplicationError::DeliveryNotFound(DeliveryId::new("delivery-1")),
            Failure::Rejected("not_found"),
        ),
        (
            DeliveryApplicationError::DeliveryAlreadyExists(DeliveryId::new("delivery-1")),
            Failure::Rejected("already_exists"),
        ),
        (
            DeliveryApplicationError::ConcurrentModification,
            Failure::Error("concurrent_modification"),
        ),
        (
            DeliveryApplicationError::DependencyUnavailable("spicedb: secret".to_owned()),
            Failure::Error("unavailable"),
        ),
        (
            DeliveryApplicationError::Transaction(UnitOfWorkError::CommitOutcomeUnknown),
            Failure::Error("transaction"),
        ),
        (
            DeliveryApplicationError::Internal("boom".to_owned()),
            Failure::Error("internal"),
        ),
    ];
    for (error, expected) in cases {
        assert_eq!(classify(&error), expected, "{error}");
    }
}
