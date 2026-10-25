use opentelemetry::metrics::MeterProvider as _;
use opentelemetry_sdk::metrics::data::{AggregatedMetrics, MetricData};
use opentelemetry_sdk::metrics::{InMemoryMetricExporter, PeriodicReader, SdkMeterProvider};

use super::*;

struct Harness {
    provider: SdkMeterProvider,
    exporter: InMemoryMetricExporter,
    metrics: TransitionMetrics,
}

fn harness() -> Harness {
    let exporter = InMemoryMetricExporter::default();
    let provider = SdkMeterProvider::builder()
        .with_reader(PeriodicReader::builder(exporter.clone()).build())
        .build();
    let metrics = TransitionMetrics::new(&provider.meter("test"));
    Harness {
        provider,
        exporter,
        metrics,
    }
}

/// Sorted `key=value` attributes of one data point.
type Attributes = Vec<String>;

impl Harness {
    /// `(attributes, value)` of the `nomnom.fsm.transitions` data points.
    fn points(&self) -> Vec<(Attributes, u64)> {
        self.provider.force_flush().expect("flush");
        let Some(exported) = self.exporter.get_finished_metrics().expect("metrics").pop() else {
            return Vec::new();
        };
        let Some(metric) = exported
            .scope_metrics()
            .flat_map(|scope| scope.metrics())
            .find(|metric| metric.name() == "nomnom.fsm.transitions")
        else {
            return Vec::new();
        };
        assert_eq!(metric.unit(), "{transition}");
        let AggregatedMetrics::U64(MetricData::Sum(sum)) = metric.data() else {
            panic!("u64 counter");
        };
        let mut points: Vec<_> = sum
            .data_points()
            .map(|point| {
                let mut attributes: Attributes = point
                    .attributes()
                    .map(|kv| format!("{}={}", kv.key, kv.value))
                    .collect();
                attributes.sort();
                (attributes, point.value())
            })
            .collect();
        points.sort();
        points
    }
}

fn expected(
    aggregate_type: &str,
    from: &str,
    trigger: &str,
    to: &str,
    result: &str,
    terminal: bool,
) -> Attributes {
    let mut attributes = vec![
        format!("nomnom.fsm.aggregate_type={aggregate_type}"),
        format!("nomnom.fsm.from_state={from}"),
        format!("nomnom.fsm.trigger={trigger}"),
        format!("nomnom.fsm.to_state={to}"),
        format!("nomnom.fsm.result={result}"),
        format!("nomnom.fsm.terminal={terminal}"),
    ];
    attributes.sort();
    attributes
}

#[test]
fn applied_transitions_carry_states_trigger_and_terminality() {
    let harness = harness();
    harness
        .metrics
        .applied(Transition::Delivery(TransitionResult {
            from: State::Assigned,
            trigger: Trigger::Pickup,
            to: State::PickedUp,
        }));
    harness
        .metrics
        .applied(Transition::Delivery(TransitionResult {
            from: State::PickedUp,
            trigger: Trigger::Complete,
            to: State::Delivered,
        }));
    harness
        .metrics
        .applied(Transition::Offer(OfferTransitionResult {
            from: OfferState::Pending,
            trigger: OfferTrigger::Withdraw,
            to: OfferState::Withdrawn,
        }));

    let mut points = vec![
        (
            expected(
                "delivery",
                "assigned",
                "pickup",
                "picked_up",
                "applied",
                false,
            ),
            1,
        ),
        (
            expected(
                "delivery",
                "picked_up",
                "complete",
                "delivered",
                "applied",
                true,
            ),
            1,
        ),
        (
            expected(
                "delivery_offer",
                "pending",
                "withdraw",
                "withdrawn",
                "applied",
                true,
            ),
            1,
        ),
    ];
    points.sort();
    assert_eq!(harness.points(), points);
}

#[test]
fn only_invalid_transitions_are_rejected_transitions() {
    let harness = harness();
    harness
        .metrics
        .rejected(&DeliveryApplicationError::DeliveryPrecondition(
            DeliveryError::Transition(TransitionError::InvalidTransition {
                from: State::PickedUp,
                trigger: Trigger::Cancel,
            }),
        ));
    harness
        .metrics
        .rejected(&DeliveryApplicationError::OfferPrecondition(
            DeliveryOfferError::InvalidTransition {
                from: OfferState::Accepted,
                trigger: OfferTrigger::Reject,
            },
        ));
    // A repeat, a failed guard, an actor check and other failures are not
    // transitions of the table.
    for error in [
        DeliveryApplicationError::DeliveryPrecondition(DeliveryError::Transition(
            TransitionError::AlreadyInState {
                state: State::Cancelled,
            },
        )),
        DeliveryApplicationError::DeliveryPrecondition(DeliveryError::Transition(
            TransitionError::GuardFailed {
                reason: "courier_id_required",
            },
        )),
        DeliveryApplicationError::DeliveryPrecondition(DeliveryError::CourierNotAssigned),
        DeliveryApplicationError::OfferPrecondition(DeliveryOfferError::Expired),
        DeliveryApplicationError::PermissionDenied,
    ] {
        harness.metrics.rejected(&error);
    }

    let mut points = vec![
        (
            expected(
                "delivery",
                "picked_up",
                "cancel",
                "picked_up",
                "rejected",
                false,
            ),
            1,
        ),
        (
            expected(
                "delivery_offer",
                "accepted",
                "reject",
                "accepted",
                "rejected",
                false,
            ),
            1,
        ),
    ];
    points.sort();
    assert_eq!(harness.points(), points);
}
