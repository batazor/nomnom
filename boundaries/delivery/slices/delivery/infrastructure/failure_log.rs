//! Telemetry of a reported Delivery Failure beyond the metric: one structured
//! log record (docs/observability.md, «Логи») and attributes of the active
//! span. Called by [`super::observability::ObservedOutbox`] for every
//! committed `DeliveryFailed`, so a rolled-back report writes nothing.
//!
//! The free-text detail goes to the log and the span only, never to a metric
//! attribute. It is courier free text: the domain limits it to
//! `DeliveryFailureReason::MAX_DETAIL_CHARS`, and the span gets at most
//! [`SPAN_DETAIL_CHARS`] of it. `trace_id`/`span_id` come from the active
//! OpenTelemetry context and are absent outside a span (the process does not
//! start traces yet, so today they are absent).

use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

use opentelemetry::KeyValue;
use opentelemetry::trace::{TraceContextExt, get_active_span};
use serde_json::{Map, Value};

use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{DeliveryFailureReason, DeliveryId, OrderId};

/// Span attribute: code of the Delivery Failure Reason catalog.
pub const SPAN_REASON: &str = "nomnom.delivery.failure.reason";
/// Span attribute: free-text detail, truncated to [`SPAN_DETAIL_CHARS`].
pub const SPAN_DETAIL: &str = "nomnom.delivery.failure.detail";
pub const SPAN_DETAIL_CHARS: usize = 256;

/// Stable operation name of the log record.
pub const FAILURE_REPORTED_EVENT: &str = "delivery.failure_reported";

/// Destination of one serialized log line.
pub type LogWriter = Arc<dyn Fn(&str) + Send + Sync>;

/// Facts of one committed Delivery Failure.
pub struct ReportedFailure<'a> {
    pub delivery_id: &'a DeliveryId,
    pub order_id: &'a OrderId,
    pub courier_id: Option<&'a CourierId>,
    pub reason: &'a DeliveryFailureReason,
}

/// Structured JSON log of the process, one record per line.
#[derive(Clone)]
pub struct FailureLog {
    service_name: String,
    write: LogWriter,
    now: Arc<dyn Fn() -> SystemTime + Send + Sync>,
}

impl FailureLog {
    /// Writes to stdout; `service.name` is `OTEL_SERVICE_NAME` when set,
    /// otherwise `default_service_name`.
    #[must_use]
    pub fn stdout(default_service_name: &str) -> Self {
        let service_name = std::env::var("OTEL_SERVICE_NAME")
            .ok()
            .filter(|name| !name.trim().is_empty())
            .unwrap_or_else(|| default_service_name.to_owned());
        Self::new(
            &service_name,
            Arc::new(|line| println!("{line}")),
            Arc::new(SystemTime::now),
        )
    }

    #[must_use]
    pub fn new(
        service_name: &str,
        write: LogWriter,
        now: Arc<dyn Fn() -> SystemTime + Send + Sync>,
    ) -> Self {
        Self {
            service_name: service_name.to_owned(),
            write,
            now,
        }
    }

    /// Logs the failure at `info` and annotates the active span.
    pub fn record(&self, failure: &ReportedFailure<'_>) {
        let code = failure.reason.as_str();
        let detail = failure.reason.detail().unwrap_or_default();
        get_active_span(|span| {
            span.set_attribute(KeyValue::new(SPAN_REASON, code));
            span.set_attribute(KeyValue::new(
                SPAN_DETAIL,
                detail.chars().take(SPAN_DETAIL_CHARS).collect::<String>(),
            ));
        });

        let mut record = Map::new();
        let mut field = |key: &str, value: Value| {
            record.insert(key.to_owned(), value);
        };
        field("timestamp", rfc3339((self.now)()).into());
        field("severity", "INFO".into());
        field("message", "delivery failure reported".into());
        field(
            "service.namespace",
            crate::pkg::telemetry::SERVICE_NAMESPACE.into(),
        );
        field("service.name", self.service_name.clone().into());
        field("event", FAILURE_REPORTED_EVENT.into());
        field("delivery_id", failure.delivery_id.as_str().into());
        field("order_id", failure.order_id.as_str().into());
        if let Some(courier_id) = failure.courier_id {
            field("courier_id", courier_id.as_str().into());
        }
        field("reason", code.into());
        field("detail", detail.into());
        let context = opentelemetry::Context::current();
        let span = context.span();
        let span_context = span.span_context();
        if span_context.is_valid() {
            field("trace_id", span_context.trace_id().to_string().into());
            field("span_id", span_context.span_id().to_string().into());
        }
        (self.write)(&Value::Object(record).to_string());
    }
}

/// UTC `YYYY-MM-DDTHH:MM:SS.mmmZ`.
fn rfc3339(time: SystemTime) -> String {
    let elapsed = time.duration_since(UNIX_EPOCH).unwrap_or_default();
    let seconds = i64::try_from(elapsed.as_secs()).unwrap_or(i64::MAX);
    let (days, of_day) = (seconds.div_euclid(86_400), seconds.rem_euclid(86_400));
    // Civil date from days since 1970-01-01 (H. Hinnant's algorithm).
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1_460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let day = doy - (153 * mp + 2) / 5 + 1;
    let month = if mp < 10 { mp + 3 } else { mp - 9 };
    let year = yoe + era * 400 + i64::from(month <= 2);
    format!(
        "{year:04}-{month:02}-{day:02}T{:02}:{:02}:{:02}.{:03}Z",
        of_day / 3_600,
        of_day % 3_600 / 60,
        of_day % 60,
        elapsed.subsec_millis()
    )
}

#[cfg(test)]
#[path = "failure_log/tests.rs"]
mod tests;
