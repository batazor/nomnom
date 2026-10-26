use std::sync::{Mutex, PoisonError};

use super::*;
use crate::pkg::config::SPICEDB_ENDPOINT_ENV;
use crate::pkg::persistence::AuthorizationRevision;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{DeliveryId, OrderId};

/// Fake SpiceDB: фиксированный ответ и журнал запросов.
struct FakeChecker {
    outcome: Result<bool, CallFailure>,
    seen: Arc<Mutex<Vec<String>>>,
    consistencies: Arc<Mutex<Vec<CheckConsistency>>>,
}

impl PermissionChecker for FakeChecker {
    fn check(&self, check: &PermissionCheck) -> Result<bool, CallFailure> {
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
        SpiceDbAuthorization::new(Box::new(checker), CheckMetrics::global())
            .with_revisions(Arc::new(FakeRevisions)),
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

fn adapter(outcome: Result<bool, CallFailure>) -> (SpiceDbAuthorization, Arc<Mutex<Vec<String>>>) {
    let seen = Arc::new(Mutex::new(Vec::new()));
    let checker = FakeChecker {
        outcome,
        seen: seen.clone(),
        consistencies: Arc::new(Mutex::new(Vec::new())),
    };
    (
        SpiceDbAuthorization::new(Box::new(checker), CheckMetrics::global()),
        seen,
    )
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
    let (authorization, _) = adapter(Err(CallFailure::Timeout(
        "spicedb check timed out".to_owned(),
    )));

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
fn empty_endpoint_is_a_config_error() {
    let Err(error) = SpiceDbAuthorization::from_config(&SpiceDbConfig::default()) else {
        panic!("an empty endpoint must not build an authorization");
    };
    assert!(error.to_string().contains(SPICEDB_ENDPOINT_ENV));
}

mod metrics {
    use std::collections::BTreeMap;

    use opentelemetry::KeyValue;
    use opentelemetry::metrics::MeterProvider as _;
    use opentelemetry_sdk::metrics::data::{AggregatedMetrics, MetricData};
    use opentelemetry_sdk::metrics::{InMemoryMetricExporter, PeriodicReader, SdkMeterProvider};

    use super::*;

    fn checker(outcome: Result<bool, CallFailure>) -> Box<dyn PermissionChecker> {
        Box::new(FakeChecker {
            outcome,
            seen: Arc::new(Mutex::new(Vec::new())),
            consistencies: Arc::new(Mutex::new(Vec::new())),
        })
    }

    /// `(permission, resource_type, result)` of one data point.
    type Key = (String, String, String);

    fn key(attributes: &[&KeyValue]) -> Key {
        // No subject or resource id.
        assert_eq!(attributes.len(), 3);
        let value = |name: &str| {
            attributes
                .iter()
                .find(|kv| kv.key.as_str() == name)
                .map(|kv| kv.value.as_str().into_owned())
                .expect(name)
        };
        (
            value("nomnom.authz.permission"),
            value("nomnom.authz.resource_type"),
            value("nomnom.authz.result"),
        )
    }

    /// `key → (checks, duration count)` of the latest export.
    fn points(
        provider: &SdkMeterProvider,
        exporter: &InMemoryMetricExporter,
    ) -> BTreeMap<Key, (u64, u64)> {
        provider.force_flush().expect("flush");
        let exported = exporter
            .get_finished_metrics()
            .expect("metrics")
            .pop()
            .expect("export");
        let mut points = BTreeMap::<Key, (u64, u64)>::new();
        for metric in exported.scope_metrics().flat_map(|scope| scope.metrics()) {
            match (metric.name(), metric.data()) {
                ("nomnom.authz.checks", AggregatedMetrics::U64(MetricData::Sum(sum))) => {
                    assert_eq!(metric.unit(), "{check}");
                    for point in sum.data_points() {
                        let attributes: Vec<_> = point.attributes().collect();
                        points.entry(key(&attributes)).or_default().0 = point.value();
                    }
                }
                (
                    "nomnom.authz.check.duration",
                    AggregatedMetrics::F64(MetricData::Histogram(histogram)),
                ) => {
                    assert_eq!(metric.unit(), "s");
                    for point in histogram.data_points() {
                        let attributes: Vec<_> = point.attributes().collect();
                        points.entry(key(&attributes)).or_default().1 = point.count();
                    }
                }
                (name, _) => panic!("unexpected metric {name}"),
            }
        }
        points
    }

    fn once(permission: &str, resource_type: &str, result: &str) -> (Key, (u64, u64)) {
        (
            (
                permission.to_owned(),
                resource_type.to_owned(),
                result.to_owned(),
            ),
            (1, 1),
        )
    }

    #[test]
    fn every_decision_of_spicedb_is_counted_by_result() {
        let exporter = InMemoryMetricExporter::default();
        let provider = SdkMeterProvider::builder()
            .with_reader(PeriodicReader::builder(exporter.clone()).build())
            .build();
        let metrics = CheckMetrics::new(&provider.meter("test"));
        let adapter = |outcome| SpiceDbAuthorization::new(checker(outcome), metrics.clone());

        assert_eq!(create_delivery(&adapter(Ok(true))), Ok(()));
        assert_eq!(
            CourierAuthorization::check(
                &adapter(Ok(false)),
                &Subject::User("user-1".to_owned()),
                CourierPermission::SetCourierAvailability,
                &CourierResource::Courier(CourierId::new("courier-2")),
            ),
            Err(AuthorizationError::Denied)
        );
        assert!(create_delivery(&adapter(Err(CallFailure::Timeout("slow".to_owned())))).is_err());
        assert!(create_delivery(&adapter(Err(CallFailure::Failed("down".to_owned())))).is_err());
        // An unreadable revision fails closed before SpiceDB.
        assert!(
            DeliveryAuthorization::check(
                &adapter(Ok(true)).with_revisions(Arc::new(FakeRevisions)),
                &Subject::User("user-1".to_owned()),
                DeliveryPermission::ConfirmPickup,
                &DeliveryResource::Delivery(DeliveryId::new("broken")),
            )
            .is_err()
        );
        // An unmapped combination never reaches SpiceDB.
        assert_eq!(
            CourierAuthorization::check(
                &adapter(Ok(true)),
                &worker(),
                CourierPermission::UpdateCourierLocation,
                &CourierResource::Platform,
            ),
            Err(AuthorizationError::Denied)
        );

        assert_eq!(
            points(&provider, &exporter),
            BTreeMap::from([
                once("coordinate_delivery", "platform", "allowed"),
                once("act", "courier", "denied"),
                once("coordinate_delivery", "platform", "timeout"),
                once("coordinate_delivery", "platform", "error"),
                once("act_as_courier", "delivery", "error"),
            ])
        );
    }
}
