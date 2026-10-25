//! Outbox metrics of ADR 13 (docs/observability/metric-inventory.md#outbox).
//! Names after OTLP → Mimir translation:
//! `nomnom_outbox_pending_messages`, `nomnom_outbox_oldest_pending_age_seconds`,
//! `nomnom_outbox_publish_retries_total`, plus the publish duration and the
//! `occurred_at` → acknowledgement lag histograms of ADR 31. Names and attributes
//! follow the registry ops/semconv (make semconv-check): `messaging.destination.name`
//! (topic), `nomnom.event.type`, `nomnom.error.class`, `nomnom.outcome`; never
//! `event_id` or `aggregate_id`.

use std::time::Duration;

use opentelemetry::KeyValue;
use opentelemetry::metrics::{Counter, Gauge, Histogram, Meter};

use crate::pkg::telemetry::SECONDS_BUCKETS;

const DESTINATION: &str = "messaging.destination.name";
const EVENT_TYPE: &str = "nomnom.event.type";
const ERROR_CLASS: &str = "nomnom.error.class";
const OUTCOME: &str = "nomnom.outcome";

/// Instrumentation scope of the relay.
pub const METER_NAME: &str = "nomnom.delivery.outbox";

#[derive(Clone)]
pub struct RelayMetrics {
    pending: Gauge<u64>,
    oldest_pending_age: Gauge<f64>,
    retries: Counter<u64>,
    duration: Histogram<f64>,
    lag: Histogram<f64>,
}

impl RelayMetrics {
    /// Instruments of the global MeterProvider (no-op without telemetry).
    #[must_use]
    pub fn global() -> Self {
        Self::new(&opentelemetry::global::meter(METER_NAME))
    }

    #[must_use]
    pub fn new(meter: &Meter) -> Self {
        Self {
            pending: meter
                .u64_gauge("nomnom.outbox.pending.messages")
                .with_unit("{message}")
                .with_description("Unpublished Outbox rows.")
                .build(),
            oldest_pending_age: meter
                .f64_gauge("nomnom.outbox.oldest_pending.age")
                .with_unit("s")
                .with_description("Age of the oldest unpublished Outbox row.")
                .build(),
            retries: meter
                .u64_counter("nomnom.outbox.publish.retries")
                .with_unit("{retry}")
                .with_description("Failed publications; the row is retried next cycle.")
                .build(),
            duration: meter
                .f64_histogram("nomnom.outbox.publish.duration")
                .with_unit("s")
                .with_description("Time from send to broker acknowledgement or failure.")
                .with_boundaries(SECONDS_BUCKETS.to_vec())
                .build(),
            lag: meter
                .f64_histogram("nomnom.outbox.publish.lag")
                .with_unit("s")
                .with_description("Time from occurred_at to broker acknowledgement.")
                .with_boundaries(SECONDS_BUCKETS.to_vec())
                .build(),
        }
    }

    pub fn backlog(&self, stream: &str, pending: u64, oldest_age_seconds: f64) {
        let attributes = [KeyValue::new(DESTINATION, stream.to_owned())];
        self.pending.record(pending, &attributes);
        self.oldest_pending_age
            .record(oldest_age_seconds.max(0.0), &attributes);
    }

    pub fn published(&self, stream: &str, event_type: &str, took: Duration, lag_seconds: f64) {
        let attributes = [
            KeyValue::new(DESTINATION, stream.to_owned()),
            KeyValue::new(EVENT_TYPE, event_type.to_owned()),
            KeyValue::new(OUTCOME, "success"),
        ];
        self.duration.record(took.as_secs_f64(), &attributes);
        self.lag.record(lag_seconds.max(0.0), &attributes[..2]);
    }

    pub fn failed(&self, stream: &str, event_type: &str, error_class: &str, took: Duration) {
        self.retries.add(
            1,
            &[
                KeyValue::new(DESTINATION, stream.to_owned()),
                KeyValue::new(EVENT_TYPE, event_type.to_owned()),
                KeyValue::new(ERROR_CLASS, error_class.to_owned()),
            ],
        );
        self.duration.record(
            took.as_secs_f64(),
            &[
                KeyValue::new(DESTINATION, stream.to_owned()),
                KeyValue::new(EVENT_TYPE, event_type.to_owned()),
                KeyValue::new(OUTCOME, "error"),
            ],
        );
    }
}
