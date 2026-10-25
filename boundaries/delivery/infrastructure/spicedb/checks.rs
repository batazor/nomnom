//! Metrics of the authorization port (authz section of the inventory
//! ops/grafana/inventory; registry ops/semconv/model/authz.yaml): every
//! decision of [`SpiceDbAuthorization`](super::SpiceDbAuthorization) on a
//! mapped check records one `nomnom.authz.checks` and one
//! `nomnom.authz.check.duration` (the revision read of FR-447 included; a
//! revision that cannot be read fails closed as `error`).
//!
//! Attributes: `nomnom.authz.permission` and `nomnom.authz.resource_type` —
//! the SpiceDB permission and resource type of the check, `nomnom.authz.result`
//! — `allowed` | `denied` (a business result) | `error` | `timeout` (a
//! technical failure, the check fails closed). No subject or resource id. A
//! combination without a mapping is denied before SpiceDB and records nothing.

use std::time::Instant;

use opentelemetry::KeyValue;
use opentelemetry::metrics::{Counter, Histogram, Meter};

use crate::pkg::telemetry::SECONDS_BUCKETS;

use super::mapping::PermissionCheck;

/// Instrumentation scope of the authorization metrics.
pub const AUTHZ_METER_NAME: &str = "nomnom.delivery.authz";

/// Result of one check (`nomnom.authz.result`).
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(super) enum CheckResult {
    Allowed,
    Denied,
    Error,
    Timeout,
}

impl CheckResult {
    fn as_str(self) -> &'static str {
        match self {
            Self::Allowed => "allowed",
            Self::Denied => "denied",
            Self::Error => "error",
            Self::Timeout => "timeout",
        }
    }
}

/// Instruments of the authorization checks of one process.
#[derive(Clone)]
pub struct CheckMetrics {
    checks: Counter<u64>,
    duration: Histogram<f64>,
}

impl CheckMetrics {
    /// Instruments of the global MeterProvider (no-op until the pipeline
    /// starts).
    #[must_use]
    pub fn global() -> Self {
        Self::new(&opentelemetry::global::meter(AUTHZ_METER_NAME))
    }

    #[must_use]
    pub fn new(meter: &Meter) -> Self {
        Self {
            checks: meter
                .u64_counter("nomnom.authz.checks")
                .with_unit("{check}")
                .with_description("Authorization checks by result.")
                .build(),
            duration: meter
                .f64_histogram("nomnom.authz.check.duration")
                .with_unit("s")
                .with_description("Duration of one authorization check.")
                .with_boundaries(SECONDS_BUCKETS.to_vec())
                .build(),
        }
    }

    pub(super) fn record(&self, check: &PermissionCheck, result: CheckResult, started: Instant) {
        let attributes = [
            KeyValue::new("nomnom.authz.permission", check.permission),
            KeyValue::new("nomnom.authz.resource_type", check.resource_type),
            KeyValue::new("nomnom.authz.result", result.as_str()),
        ];
        self.checks.add(1, &attributes);
        self.duration
            .record(started.elapsed().as_secs_f64(), &attributes);
    }
}
