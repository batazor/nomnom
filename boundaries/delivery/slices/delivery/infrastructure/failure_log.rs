//! Telemetry of a reported Delivery Failure beyond the metric: one structured
//! log record (docs/observability.md, «Логи») and attributes of the active
//! span. Called by [`super::observability::ObservedOutbox`] for every
//! committed `DeliveryFailed`, so a rolled-back report writes nothing.
//!
//! The free-text detail goes to the log and the span only, never to a metric
//! attribute. It is courier free text: the domain limits it to
//! `DeliveryFailureReason::MAX_DETAIL_CHARS`, and the span gets at most
//! [`SPAN_DETAIL_CHARS`] of it. The record goes through the process logger
//! (`pkg/telemetry/log`), so it has the common JSON schema; `trace_id` /
//! `span_id` are those of the active span (the use case of the report) and
//! are absent outside a span.

use opentelemetry::KeyValue;
use opentelemetry::trace::get_active_span;

use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{DeliveryFailureReason, DeliveryId, OrderId};

/// Span attribute: code of the Delivery Failure Reason catalog.
pub const SPAN_REASON: &str = "nomnom.delivery.failure.reason";
/// Span attribute: free-text detail, truncated to [`SPAN_DETAIL_CHARS`].
pub const SPAN_DETAIL: &str = "nomnom.delivery.failure.detail";
pub const SPAN_DETAIL_CHARS: usize = 256;

/// Stable operation name of the log record.
pub const FAILURE_REPORTED_EVENT: &str = "delivery.failure_reported";

/// Facts of one committed Delivery Failure.
pub struct ReportedFailure<'a> {
    pub delivery_id: &'a DeliveryId,
    pub order_id: &'a OrderId,
    pub courier_id: Option<&'a CourierId>,
    pub reason: &'a DeliveryFailureReason,
}

/// Log record and span attributes of committed Delivery Failures.
#[derive(Clone, Copy, Debug, Default)]
pub struct FailureLog;

impl FailureLog {
    /// Logs the failure at `INFO` and annotates the active span.
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
        match failure.courier_id {
            Some(courier_id) => tracing::info!(
                operation = FAILURE_REPORTED_EVENT,
                delivery_id = failure.delivery_id.as_str(),
                order_id = failure.order_id.as_str(),
                courier_id = courier_id.as_str(),
                reason = code,
                detail,
                "delivery failure reported"
            ),
            None => tracing::info!(
                operation = FAILURE_REPORTED_EVENT,
                delivery_id = failure.delivery_id.as_str(),
                order_id = failure.order_id.as_str(),
                reason = code,
                detail,
                "delivery failure reported"
            ),
        }
    }
}

#[cfg(test)]
#[path = "failure_log/tests.rs"]
mod tests;
