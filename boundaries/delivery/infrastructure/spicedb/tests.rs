use std::sync::{Mutex, PoisonError};

use super::*;
use crate::pkg::persistence::AuthorizationRevision;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{DeliveryId, OrderId};

/// Fake SpiceDB: фиксированный ответ и журнал запросов.
struct FakeChecker {
    outcome: Result<bool, String>,
    seen: Arc<Mutex<Vec<String>>>,
    consistencies: Arc<Mutex<Vec<CheckConsistency>>>,
}

impl PermissionChecker for FakeChecker {
    fn check(&self, check: &PermissionCheck) -> Result<bool, String> {
        self.seen
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .push(check.to_string());
        self.consistencies
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .push(check.consistency.clone());
        self.outcome.clone()
    }
}

/// Revisions: `courier-1` и `delivery-1` с revision, `broken` — ошибка
/// хранилища, `platform:nomnom` тоже «имеет» revision, чтобы проверить,
/// что адаптер её не читает.
struct FakeRevisions;

impl RevisionReader for FakeRevisions {
    fn revision(
        &self,
        resource_type: &str,
        resource_id: &str,
    ) -> Result<Option<AuthorizationRevision>, String> {
        match (resource_type, resource_id) {
            (_, "broken") => Err("store offline".to_owned()),
            ("courier", "courier-1") => Ok(Some(AuthorizationRevision::new("zed-c"))),
            ("delivery", "delivery-1") => Ok(Some(AuthorizationRevision::new("zed-d"))),
            ("platform", _) => Ok(Some(AuthorizationRevision::new("zed-p"))),
            _ => Ok(None),
        }
    }
}

fn revisioned() -> (SpiceDbAuthorization, Arc<Mutex<Vec<CheckConsistency>>>) {
    let consistencies = Arc::new(Mutex::new(Vec::new()));
    let checker = FakeChecker {
        outcome: Ok(true),
        seen: Arc::new(Mutex::new(Vec::new())),
        consistencies: consistencies.clone(),
    };
    (
        SpiceDbAuthorization::new(Box::new(checker)).with_revisions(Arc::new(FakeRevisions)),
        consistencies,
    )
}

fn act_as_courier(
    authorization: &SpiceDbAuthorization,
    delivery: &str,
) -> Result<(), AuthorizationError> {
    DeliveryAuthorization::check(
        authorization,
        &Subject::User("courier-1".to_owned()),
        DeliveryPermission::ConfirmPickup,
        &DeliveryResource::Delivery(DeliveryId::new(delivery)),
    )
}

/// FR-447, AC-484: stored revision — `at_least_as_fresh`, без неё и для
/// `platform:nomnom` — `minimize_latency`.
#[test]
fn consistency_follows_stored_revision() {
    let (authorization, consistencies) = revisioned();

    assert_eq!(act_as_courier(&authorization, "delivery-1"), Ok(()));
    assert_eq!(act_as_courier(&authorization, "delivery-2"), Ok(()));
    assert_eq!(
        CourierAuthorization::check(
            &authorization,
            &Subject::User("courier-1".to_owned()),
            CourierPermission::SetCourierAvailability,
            &CourierResource::Courier(CourierId::new("courier-1")),
        ),
        Ok(())
    );
    assert_eq!(create_delivery(&authorization), Ok(()));

    assert_eq!(
        *consistencies.lock().unwrap_or_else(PoisonError::into_inner),
        vec![
            CheckConsistency::AtLeastAsFresh(AuthorizationRevision::new("zed-d")),
            CheckConsistency::MinimizeLatency,
            CheckConsistency::AtLeastAsFresh(AuthorizationRevision::new("zed-c")),
            CheckConsistency::MinimizeLatency,
        ]
    );
}

/// FR-448: revision не прочитана — authorization unavailable без
/// обращения к SpiceDB.
#[test]
fn revision_read_failure_fails_closed_as_unavailable() {
    let (authorization, consistencies) = revisioned();

    assert!(matches!(
        act_as_courier(&authorization, "broken"),
        Err(AuthorizationError::Unavailable(_))
    ));
    assert!(
        consistencies
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .is_empty()
    );
}

fn adapter(outcome: Result<bool, String>) -> (SpiceDbAuthorization, Arc<Mutex<Vec<String>>>) {
    let seen = Arc::new(Mutex::new(Vec::new()));
    let checker = FakeChecker {
        outcome,
        seen: seen.clone(),
        consistencies: Arc::new(Mutex::new(Vec::new())),
    };
    (SpiceDbAuthorization::new(Box::new(checker)), seen)
}

fn worker() -> Subject {
    Subject::Service("orders-worker".to_owned())
}

fn create_delivery(authorization: &SpiceDbAuthorization) -> Result<(), AuthorizationError> {
    DeliveryAuthorization::check(
        authorization,
        &worker(),
        DeliveryPermission::CreateDelivery,
        &DeliveryResource::Order(OrderId::new("order-1")),
    )
}

#[test]
fn has_permission_allows() {
    let (authorization, seen) = adapter(Ok(true));

    assert_eq!(create_delivery(&authorization), Ok(()));
    assert_eq!(
        *seen.lock().unwrap_or_else(PoisonError::into_inner),
        vec!["platform:nomnom#coordinate_delivery@service:orders-worker".to_owned()]
    );
}

#[test]
fn no_permission_denies() {
    let (authorization, _) = adapter(Ok(false));

    assert_eq!(
        create_delivery(&authorization),
        Err(AuthorizationError::Denied)
    );
}

#[test]
fn spicedb_error_fails_closed_as_unavailable() {
    let (authorization, _) = adapter(Err("spicedb check timed out".to_owned()));

    assert!(matches!(
        create_delivery(&authorization),
        Err(AuthorizationError::Unavailable(_))
    ));
    assert!(matches!(
        CourierAuthorization::check(
            &authorization,
            &worker(),
            CourierPermission::RegisterCourier,
            &CourierResource::Platform,
        ),
        Err(AuthorizationError::Unavailable(_))
    ));
}

#[test]
fn unmapped_check_is_denied_without_calling_spicedb() {
    let (authorization, seen) = adapter(Ok(true));

    assert_eq!(
        CourierAuthorization::check(
            &authorization,
            &worker(),
            CourierPermission::UpdateCourierLocation,
            &CourierResource::Platform,
        ),
        Err(AuthorizationError::Denied)
    );
    assert_eq!(
        DeliveryAuthorization::check(
            &authorization,
            &worker(),
            DeliveryPermission::ConfirmPickup,
            &DeliveryResource::Order(OrderId::new("order-1")),
        ),
        Err(AuthorizationError::Denied)
    );
    assert!(
        seen.lock()
            .unwrap_or_else(PoisonError::into_inner)
            .is_empty()
    );
}

#[test]
fn courier_permission_checks_courier_act() {
    let (authorization, seen) = adapter(Ok(true));

    assert_eq!(
        CourierAuthorization::check(
            &authorization,
            &Subject::User("user-1".to_owned()),
            CourierPermission::SetCourierAvailability,
            &CourierResource::Courier(CourierId::new("courier-1")),
        ),
        Ok(())
    );
    assert_eq!(
        *seen.lock().unwrap_or_else(PoisonError::into_inner),
        vec!["courier:courier-1#act@user:user-1".to_owned()]
    );
}

#[test]
fn empty_endpoint_keeps_local_authorization() {
    assert!(matches!(
        SpiceDbAuthorization::from_config(&SpiceDbConfig::default()),
        Ok(None)
    ));
}
