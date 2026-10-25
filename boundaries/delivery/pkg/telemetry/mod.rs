//! OpenTelemetry metrics процесса (ADR 9, ADR 13), по образцу
//! `orders/pkg/telemetry`: MeterProvider с resource `service.name` /
//! `service.namespace` и OTLP gRPC exporter в Alloy. Экспорт включается только
//! при заданном `OTEL_EXPORTER_OTLP_ENDPOINT` (или
//! `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`); без него instruments пишут в
//! no-op provider. `OTEL_SERVICE_NAME` и `OTEL_RESOURCE_ATTRIBUTES`
//! переопределяют resource.

use opentelemetry::KeyValue;
use opentelemetry_otlp::MetricExporter;
use opentelemetry_sdk::Resource;
use opentelemetry_sdk::metrics::SdkMeterProvider;

/// `service.namespace` всех процессов (docs/observability.md).
pub const SERVICE_NAMESPACE: &str = "nomnom";

const ENDPOINT_ENV: &str = "OTEL_EXPORTER_OTLP_ENDPOINT";
const METRICS_ENDPOINT_ENV: &str = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT";
const SERVICE_NAME_ENV: &str = "OTEL_SERVICE_NAME";

/// Границы histograms длительностей в секундах (как `SecondsBuckets` orders).
pub const SECONDS_BUCKETS: [f64; 14] = [
    0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0, 300.0,
];

/// Metrics pipeline процесса; `shutdown` выгружает накопленные точки.
pub struct Telemetry {
    provider: Option<SdkMeterProvider>,
}

impl Telemetry {
    /// Экспорт включён.
    #[must_use]
    pub fn exporting(&self) -> bool {
        self.provider.is_some()
    }

    pub fn shutdown(self) {
        if let Some(provider) = self.provider
            && let Err(error) = provider.shutdown()
        {
            eprintln!("telemetry shutdown: {error}");
        }
    }
}

fn configured(name: &str) -> bool {
    std::env::var(name).is_ok_and(|value| !value.trim().is_empty())
}

/// Собирает pipeline и регистрирует глобальный MeterProvider. Вызывается
/// внутри tokio runtime: tonic exporter держит соединение на нём.
pub fn start(service_name: &str) -> Result<Telemetry, String> {
    if !configured(ENDPOINT_ENV) && !configured(METRICS_ENDPOINT_ENV) {
        return Ok(Telemetry { provider: None });
    }
    // Endpoint, headers и timeout exporter читает из OTEL_EXPORTER_OTLP_*.
    let exporter = MetricExporter::builder()
        .with_tonic()
        .build()
        .map_err(|error| format!("create OTLP metric exporter: {error}"))?;
    let provider = SdkMeterProvider::builder()
        .with_periodic_exporter(exporter)
        .with_resource(resource(service_name))
        .build();
    opentelemetry::global::set_meter_provider(provider.clone());
    Ok(Telemetry {
        provider: Some(provider),
    })
}

fn resource(service_name: &str) -> Resource {
    let builder =
        Resource::builder().with_attribute(KeyValue::new("service.namespace", SERVICE_NAMESPACE));
    // OTEL_SERVICE_NAME читает detector builder'а; имя процесса — значение по умолчанию.
    if configured(SERVICE_NAME_ENV) {
        builder.build()
    } else {
        builder.with_service_name(service_name.to_owned()).build()
    }
}
