//! Telemetry of application use cases from the outside
//! (docs/observability.md#use-cases), the Rust counterpart of
//! `orders/pkg/telemetry` `Instrument`: one span `usecase <name>` and one
//! measurement of `nomnom.usecase.duration` (registry
//! ops/semconv/model/usecase.yaml) per call. Use cases and domains do not
//! import OpenTelemetry: the slice decorators in
//! `slices/<slice>/infrastructure/observed_use_cases.rs` wrap the use case
//! port and classify its errors.
//!
//! Attributes: `nomnom.usecase.name`, `nomnom.usecase.kind` (`command` |
//! `query`), `nomnom.outcome` (`success` | `rejected` | `error`) and
//! `error.type` — a closed set of the slice, only when the call failed. No
//! identifiers, subjects, payloads or error texts: they may contain input.

use std::time::Instant;

use opentelemetry::global::BoxedTracer;
use opentelemetry::metrics::{Histogram, Meter};
use opentelemetry::trace::{Status, TraceContextExt as _, Tracer as _};
use opentelemetry::{Context, KeyValue};

/// Instrumentation scope of the use case telemetry.
pub const INSTRUMENTATION: &str = "nomnom.delivery.usecase";

/// Buckets of `nomnom.usecase.duration`, as in `orders`.
const BUCKETS: [f64; 12] = [
    0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0,
];

/// Commands change state, queries only read.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Kind {
    Command,
    Query,
}

impl Kind {
    #[must_use]
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Command => "command",
            Self::Query => "query",
        }
    }
}

/// Name of a use case in telemetry: stable `snake_case`, e.g. `create_delivery`.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct UseCase {
    pub name: &'static str,
    pub kind: Kind,
}

impl UseCase {
    #[must_use]
    pub const fn command(name: &'static str) -> Self {
        Self {
            name,
            kind: Kind::Command,
        }
    }

    #[must_use]
    pub const fn query(name: &'static str) -> Self {
        Self {
            name,
            kind: Kind::Query,
        }
    }
}

/// How a failed call is reported.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Failure {
    /// An expected refusal by the rules of the use case: invalid input,
    /// denied access, missing resource, forbidden transition, domain rule.
    Rejected(&'static str),
    /// A technical failure: infrastructure, a version conflict to retry,
    /// an unclassified error.
    Error(&'static str),
}

impl Failure {
    fn outcome(self) -> &'static str {
        match self {
            Self::Rejected(_) => "rejected",
            Self::Error(_) => "error",
        }
    }

    fn error_type(self) -> &'static str {
        match self {
            Self::Rejected(error_type) | Self::Error(error_type) => error_type,
        }
    }
}

/// `error.type` of an error the slice does not classify.
pub const ERROR_TYPE_INTERNAL: &str = "internal";

/// Tracer and histogram shared by the decorated use cases of a process.
pub struct UseCaseTelemetry {
    tracer: BoxedTracer,
    duration: Histogram<f64>,
}

impl UseCaseTelemetry {
    /// Instruments of the global providers (no-op until the pipeline starts).
    #[must_use]
    pub fn global() -> Self {
        Self::new(
            opentelemetry::global::tracer(INSTRUMENTATION),
            &opentelemetry::global::meter(INSTRUMENTATION),
        )
    }

    #[must_use]
    pub fn new(tracer: BoxedTracer, meter: &Meter) -> Self {
        Self {
            tracer,
            duration: meter
                .f64_histogram("nomnom.usecase.duration")
                .with_unit("s")
                .with_description("Duration of one application use case call.")
                .with_boundaries(BUCKETS.to_vec())
                .build(),
        }
    }

    /// Runs `call` inside the span of `use_case` and records its duration;
    /// the result is returned unchanged. Use cases are synchronous, so the
    /// span is the current context for every port they call (database,
    /// SpiceDB, Outbox).
    pub fn observe<T, E>(
        &self,
        use_case: UseCase,
        classify: impl FnOnce(&E) -> Failure,
        call: impl FnOnce() -> Result<T, E>,
    ) -> Result<T, E> {
        let mut attributes = vec![
            KeyValue::new("nomnom.usecase.name", use_case.name),
            KeyValue::new("nomnom.usecase.kind", use_case.kind.as_str()),
        ];
        let span = self
            .tracer
            .span_builder(format!("usecase {}", use_case.name))
            .with_attributes(attributes.clone())
            .start(&self.tracer);
        let context = Context::current_with_span(span);
        let started = Instant::now();
        let result = {
            let _attached = context.clone().attach();
            call()
        };
        let elapsed = started.elapsed().as_secs_f64();

        let failure = result.as_ref().err().map(classify);
        attributes.push(KeyValue::new(
            "nomnom.outcome",
            failure.map_or("success", Failure::outcome),
        ));
        if let Some(failure) = failure {
            attributes.push(KeyValue::new("error.type", failure.error_type()));
        }
        let span = context.span();
        span.set_attributes(attributes[2..].iter().cloned());
        if let Some(Failure::Error(error_type)) = failure {
            span.set_status(Status::error(error_type));
        }
        span.end();
        self.duration.record(elapsed, &attributes);
        result
    }
}

#[cfg(test)]
#[path = "usecase/tests.rs"]
mod tests;
