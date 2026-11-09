//! Telemetry of the consumer (registry ops/semconv/model/messaging.yaml), the
//! counterpart of the middleware of the `orders` consumer and the metrics of
//! the `restaurants` one. No identifiers, payloads or error texts in
//! attributes: they may contain input.

use std::time::Duration;

use opentelemetry::KeyValue;
use opentelemetry::metrics::{Counter, Histogram, Meter};

/// Instrumentation scope of the consumer telemetry.
pub(super) const INSTRUMENTATION: &str = "nomnom.delivery.messaging";

/// `nomnom.error.class` of a record that cannot be read or does not match its
/// schema, before any handler.
pub(super) const CLASS_DESERIALIZATION: &str = "deserialization";
/// `nomnom.error.class` of a permanent error or panic of the handler.
pub(super) const CLASS_PERMANENT: &str = "permanent";
/// `nomnom.error.class` of a message abandoned by the stopping process while a
/// dependency was failing. Until then the partition waits: see
/// `nomnom.messaging.retry.attempts`.
const CLASS_TRANSIENT: &str = "transient";

/// How the processing of one message ended.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(super) enum Outcome {
    Processed,
    /// The Inbox already has the event.
    Duplicate,
    /// Sent to `<stream>.dlq`, with its `nomnom.error.class`.
    DeadLettered(&'static str),
    /// The process stops before the message was handled; its offset is not
    /// committed.
    Stopped,
}

/// Attributes every messaging metric of one message carries.
pub(super) struct MessageLabels {
    pub handler: &'static str,
    pub event_type: String,
    pub topic: &'static str,
}

impl MessageLabels {
    fn attributes(&self) -> Vec<KeyValue> {
        vec![
            KeyValue::new("nomnom.messaging.handler", self.handler),
            KeyValue::new("nomnom.event.type", self.event_type.clone()),
            KeyValue::new("messaging.destination.name", self.topic),
        ]
    }
}

pub(super) struct ConsumerTelemetry {
    consumed: Counter<u64>,
    duration: Histogram<f64>,
    age: Histogram<f64>,
    retries: Counter<u64>,
    poison: Counter<u64>,
    duplicates: Counter<u64>,
}

impl ConsumerTelemetry {
    pub(super) fn new(meter: &Meter) -> Self {
        Self {
            consumed: meter
                .u64_counter("messaging.client.consumed.messages")
                .with_unit("{message}")
                .with_description("Messages delivered to the consumer.")
                .build(),
            duration: meter
                .f64_histogram("nomnom.messaging.process.duration")
                .with_unit("s")
                .with_description(
                    "Duration of processing one inbound Integration Event, retries included.",
                )
                .build(),
            age: meter
                .f64_histogram("nomnom.messaging.message.age")
                .with_unit("s")
                .with_description("now − occurred_at when the consumer receives the event.")
                .build(),
            retries: meter
                .u64_counter("nomnom.messaging.retry.attempts")
                .with_unit("{attempt}")
                .with_description("In-process retries of transient failures.")
                .build(),
            poison: meter
                .u64_counter("nomnom.messaging.poison.messages")
                .with_unit("{message}")
                .with_description("Messages sent to <stream>.dlq.")
                .build(),
            duplicates: meter
                .u64_counter("nomnom.inbox.duplicates")
                .with_unit("{message}")
                .with_description("Redeliveries completed as Inbox duplicates.")
                .build(),
        }
    }

    pub(super) fn consumed(&self, topic: &'static str, group: &str) {
        self.consumed.add(
            1,
            &[
                KeyValue::new("messaging.system", "kafka"),
                KeyValue::new("messaging.destination.name", topic),
                KeyValue::new("messaging.consumer.group.name", group.to_owned()),
            ],
        );
    }

    /// `age` — `now − occurred_at`; an event from the future counts as zero.
    pub(super) fn message_age(&self, labels: &MessageLabels, age: Duration) {
        self.age.record(age.as_secs_f64(), &labels.attributes());
    }

    pub(super) fn retried(&self, labels: &MessageLabels) {
        self.retries.add(1, &labels.attributes());
    }

    pub(super) fn finished(&self, labels: &MessageLabels, outcome: Outcome, elapsed: Duration) {
        let attributes = labels.attributes();
        let class = match outcome {
            Outcome::Processed => None,
            Outcome::Duplicate => {
                self.duplicates.add(1, &attributes);
                None
            }
            Outcome::DeadLettered(class) => Some(class),
            Outcome::Stopped => Some(CLASS_TRANSIENT),
        };
        let mut recorded = attributes;
        if let Some(class) = class {
            recorded.push(KeyValue::new("nomnom.error.class", class));
        }
        if matches!(outcome, Outcome::DeadLettered(_)) {
            self.poison.add(1, &recorded);
        }
        self.duration.record(elapsed.as_secs_f64(), &recorded);
    }
}

#[cfg(test)]
mod tests {
    use opentelemetry::metrics::MeterProvider as _;
    use opentelemetry_sdk::metrics::data::{AggregatedMetrics, MetricData, ResourceMetrics};
    use opentelemetry_sdk::metrics::{InMemoryMetricExporter, PeriodicReader, SdkMeterProvider};

    use super::*;

    const DURATION: &str = "nomnom.messaging.process.duration";
    const POISON: &str = "nomnom.messaging.poison.messages";
    const DUPLICATES: &str = "nomnom.inbox.duplicates";
    const RETRIES: &str = "nomnom.messaging.retry.attempts";
    const AGE: &str = "nomnom.messaging.message.age";
    const CONSUMED: &str = "messaging.client.consumed.messages";

    /// The exported points of a metric: sorted `name=value` attributes and the
    /// value of a counter or the count of a histogram.
    fn points(exported: &[ResourceMetrics], name: &str) -> Vec<(Vec<String>, u64)> {
        let metric = exported
            .iter()
            .flat_map(|resource| resource.scope_metrics())
            .flat_map(|scope| scope.metrics())
            .find(|metric| metric.name() == name);
        let Some(metric) = metric else {
            return Vec::new();
        };
        let labelled = |pairs: Vec<&KeyValue>| {
            let mut labels: Vec<String> = pairs
                .into_iter()
                .map(|kv| format!("{}={}", kv.key, kv.value.as_str()))
                .collect();
            labels.sort();
            labels
        };
        let mut found: Vec<(Vec<String>, u64)> = match metric.data() {
            AggregatedMetrics::U64(MetricData::Sum(sum)) => sum
                .data_points()
                .map(|point| (labelled(point.attributes().collect()), point.value()))
                .collect(),
            AggregatedMetrics::F64(MetricData::Histogram(histogram)) => histogram
                .data_points()
                .map(|point| (labelled(point.attributes().collect()), point.count()))
                .collect(),
            _ => panic!("{name}: unexpected instrument"),
        };
        found.sort();
        found
    }

    fn labelled(class: Option<&str>) -> Vec<String> {
        let mut labels = vec![
            "messaging.destination.name=delivery.v1.delivery-events".to_owned(),
            "nomnom.event.type=nomnom.delivery.v1.DeliveryCompleted".to_owned(),
            "nomnom.messaging.handler=delivery.duration-statistics".to_owned(),
        ];
        if let Some(class) = class {
            labels.push(format!("nomnom.error.class={class}"));
        }
        labels.sort();
        labels
    }

    #[test]
    fn every_outcome_reports_its_metrics() {
        let exporter = InMemoryMetricExporter::default();
        let provider = SdkMeterProvider::builder()
            .with_reader(PeriodicReader::builder(exporter.clone()).build())
            .build();
        let telemetry = ConsumerTelemetry::new(&provider.meter(INSTRUMENTATION));
        let labels = MessageLabels {
            handler: "delivery.duration-statistics",
            event_type: "nomnom.delivery.v1.DeliveryCompleted".to_owned(),
            topic: "delivery.v1.delivery-events",
        };
        let elapsed = Duration::from_millis(5);

        telemetry.consumed("delivery.v1.delivery-events", "delivery");
        telemetry.message_age(&labels, Duration::from_secs(2));
        telemetry.retried(&labels);
        telemetry.finished(&labels, Outcome::Processed, elapsed);
        telemetry.finished(&labels, Outcome::Duplicate, elapsed);
        telemetry.finished(&labels, Outcome::DeadLettered(CLASS_PERMANENT), elapsed);
        telemetry.finished(&labels, Outcome::Stopped, elapsed);
        provider.force_flush().expect("flush metrics");
        let exported = exporter.get_finished_metrics().expect("metrics");

        assert_eq!(
            points(&exported, CONSUMED),
            [(
                vec![
                    "messaging.consumer.group.name=delivery".to_owned(),
                    "messaging.destination.name=delivery.v1.delivery-events".to_owned(),
                    "messaging.system=kafka".to_owned(),
                ],
                1
            )]
        );
        assert_eq!(points(&exported, AGE), [(labelled(None), 1)]);
        assert_eq!(points(&exported, RETRIES), [(labelled(None), 1)]);
        assert_eq!(points(&exported, DUPLICATES), [(labelled(None), 1)]);
        assert_eq!(
            points(&exported, POISON),
            [(labelled(Some("permanent")), 1)]
        );
        let mut durations = vec![
            (labelled(None), 2),
            (labelled(Some("permanent")), 1),
            (labelled(Some("transient")), 1),
        ];
        durations.sort();
        assert_eq!(points(&exported, DURATION), durations);
    }
}
