//! OpenTelemetry pipeline процесса (ADR 9, ADR 13), по образцу
//! `orders/pkg/telemetry`: один resource для traces, metrics и logs
//! (`service.namespace=nomnom`, `service.name` — имя процесса или
//! `OTEL_SERVICE_NAME`, остальное — `OTEL_RESOURCE_ATTRIBUTES`:
//! `service.version`, `service.instance.id`, `deployment.environment.name`).
//!
//! - Traces: глобальный W3C Trace Context propagator (baggage не
//!   передаётся); TracerProvider с batch OTLP gRPC exporter (ограниченная
//!   очередь SDK, 2048 spans) — только при `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`
//!   или `OTEL_EXPORTER_OTLP_ENDPOINT`.
//! - Metrics: MeterProvider с periodic OTLP gRPC exporter — только при
//!   `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` или `OTEL_EXPORTER_OTLP_ENDPOINT`;
//!   вместе с ним process runtime metrics ([`process`]).
//! - Logs: JSON в stdout ([`log`]), не зависят от endpoint.
//!
//! Без endpoint instruments и spans пишут в no-op providers, а входящий
//! `traceparent` всё равно продолжается. Endpoint, headers и timeout
//! exporters читают из стандартных `OTEL_EXPORTER_OTLP_*`. [`Telemetry::shutdown`]
//! выгружает spans и точки после drain (docs/cloud-native-runtime.md).

pub mod db;
pub mod log;
pub mod process;
pub mod rpc;
mod trace;
pub mod usecase;

pub use trace::{current_traceparent, extract_context, traceparent_of};

use opentelemetry::KeyValue;
use opentelemetry::metrics::MeterProvider as _;
use opentelemetry_otlp::{MetricExporter, SpanExporter};
use opentelemetry_sdk::Resource;
use opentelemetry_sdk::metrics::SdkMeterProvider;
use opentelemetry_sdk::propagation::TraceContextPropagator;
use opentelemetry_sdk::trace::SdkTracerProvider;

/// `service.namespace` всех процессов (docs/observability.md).
pub const SERVICE_NAMESPACE: &str = "nomnom";

const ENDPOINT_ENV: &str = "OTEL_EXPORTER_OTLP_ENDPOINT";
const METRICS_ENDPOINT_ENV: &str = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT";
const TRACES_ENDPOINT_ENV: &str = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT";
const SERVICE_NAME_ENV: &str = "OTEL_SERVICE_NAME";

/// Границы histograms длительностей в секундах (как `SecondsBuckets` orders).
pub const SECONDS_BUCKETS: [f64; 14] = [
    0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0, 300.0,
];

/// Metrics и traces pipeline процесса; `shutdown` выгружает накопленные
/// точки и spans.
pub struct Telemetry {
    provider: Option<SdkMeterProvider>,
    tracer_provider: Option<SdkTracerProvider>,
}

impl Telemetry {
    /// Экспорт metrics включён.
    #[must_use]
    pub fn exporting(&self) -> bool {
        self.provider.is_some()
    }

    /// Экспорт traces включён.
    #[must_use]
    pub fn exporting_traces(&self) -> bool {
        self.tracer_provider.is_some()
    }

    pub fn shutdown(self) {
        if let Some(provider) = self.tracer_provider
            && let Err(error) = provider.shutdown()
        {
            tracing::error!(operation = "telemetry.shutdown", error.message = %error, "telemetry traces shutdown failed");
        }
        if let Some(provider) = self.provider
            && let Err(error) = provider.shutdown()
        {
            tracing::error!(operation = "telemetry.shutdown", error.message = %error, "telemetry metrics shutdown failed");
        }
    }
}

fn configured(name: &str) -> bool {
    std::env::var(name).is_ok_and(|value| !value.trim().is_empty())
}

/// Собирает pipeline процесса и регистрирует глобальные providers и
/// propagator. Вызывается внутри tokio runtime (tonic exporters держат
/// соединение на нём) и до создания instruments: они берутся у глобальных
/// providers при сборке.
pub fn start(service_name: &str) -> Result<Telemetry, String> {
    opentelemetry::global::set_text_map_propagator(TraceContextPropagator::new());
    let resource = resource(service_name);
    let tracer_provider = if configured(ENDPOINT_ENV) || configured(TRACES_ENDPOINT_ENV) {
        let exporter = SpanExporter::builder()
            .with_tonic()
            .build()
            .map_err(|error| format!("create OTLP span exporter: {error}"))?;
        let provider = SdkTracerProvider::builder()
            .with_batch_exporter(exporter)
            .with_resource(resource.clone())
            .build();
        opentelemetry::global::set_tracer_provider(provider.clone());
        Some(provider)
    } else {
        None
    };
    let provider = if configured(ENDPOINT_ENV) || configured(METRICS_ENDPOINT_ENV) {
        let exporter = MetricExporter::builder()
            .with_tonic()
            .build()
            .map_err(|error| format!("create OTLP metric exporter: {error}"))?;
        let provider = SdkMeterProvider::builder()
            .with_periodic_exporter(exporter)
            .with_resource(resource)
            .build();
        opentelemetry::global::set_meter_provider(provider.clone());
        process::register(&provider.meter(process::METER_NAME), process::procfs());
        Some(provider)
    } else {
        None
    };
    Ok(Telemetry {
        provider,
        tracer_provider,
    })
}

/// Resource всех сигналов процесса; `OTEL_SERVICE_NAME` и
/// `OTEL_RESOURCE_ATTRIBUTES` читают detectors builder'а.
#[must_use]
pub fn resource(service_name: &str) -> Resource {
    let builder =
        Resource::builder().with_attribute(KeyValue::new("service.namespace", SERVICE_NAMESPACE));
    if configured(SERVICE_NAME_ENV) {
        builder.build()
    } else {
        builder.with_service_name(service_name.to_owned()).build()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn resource_has_namespace_and_default_service_name() {
        let resource = resource("delivery-test-default");
        let get = |key: &'static str| {
            resource
                .get(&opentelemetry::Key::from_static_str(key))
                .map(|value| value.as_str().into_owned())
        };
        assert_eq!(get("service.namespace").as_deref(), Some("nomnom"));
        if !configured(SERVICE_NAME_ENV) {
            assert_eq!(
                get("service.name").as_deref(),
                Some("delivery-test-default")
            );
        }
    }
}
