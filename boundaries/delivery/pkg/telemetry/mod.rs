//! OpenTelemetry pipeline процесса (ADR 9, ADR 13), по образцу
//! `orders/pkg/telemetry`. Настройки — только из файла конфигурации
//! ([`TelemetryConfig`]: `spec.telemetry`, `spec.tracing`, `spec.metrics`,
//! `spec.logging`); `OTEL_*` процесс не читает, endpoint, протокол, resource,
//! интервал, timeout и sampler передаются SDK явно и имеют приоритет над env.
//!
//! - Resource: один для traces, metrics и logs — `service.namespace=nomnom`,
//!   `service.name` (`telemetry.serviceName`, по умолчанию роль процесса),
//!   `service.instance.id` (имя pod), атрибуты `telemetry.resourceAttributes`
//!   (`service.version`, `deployment.environment.name`) и `telemetry.sdk.*`.
//! - Traces: глобальный W3C Trace Context propagator (baggage не
//!   передаётся); TracerProvider с batch OTLP exporter (ограниченная очередь
//!   SDK, 2048 spans) — только при `tracing.enabled`.
//! - Metrics: MeterProvider с periodic OTLP exporter (`metrics.exportInterval`)
//!   — только при `metrics.enabled`; вместе с ним process runtime metrics
//!   ([`process`]). Все histograms — base2 exponential
//!   ([`exponential_histograms`]): Grafana Cloud хранит их как native
//!   histograms, explicit-границ у instruments нет.
//! - OTLP: `grpc` (tonic) или `http/protobuf` (`<endpoint>/v1/traces`,
//!   `<endpoint>/v1/metrics`).
//! - Logs: JSON в stdout ([`log`]) с уровнем `logging.level`, не зависят от
//!   endpoint.
//!
//! Без экспорта instruments и spans пишут в no-op providers, а входящий
//! `traceparent` всё равно продолжается. [`Telemetry::shutdown`] выгружает
//! spans и точки после drain (docs/cloud-native-runtime.md).

pub mod db;
pub mod log;
pub mod process;
pub mod rpc;
mod trace;
pub mod usecase;

pub use trace::{current_traceparent, extract_context, traceparent_of};

use opentelemetry::KeyValue;
use opentelemetry::metrics::MeterProvider as _;
use opentelemetry_otlp::{
    MetricExporter, OTEL_EXPORTER_OTLP_TIMEOUT_DEFAULT, Protocol, SpanExporter, WithExportConfig,
    WithHttpConfig,
};
use opentelemetry_sdk::Resource;
use opentelemetry_sdk::metrics::{
    Aggregation, Instrument, InstrumentKind, PeriodicReader, SdkMeterProvider, Stream,
};
use opentelemetry_sdk::propagation::TraceContextPropagator;
use opentelemetry_sdk::resource::TelemetryResourceDetector;
use opentelemetry_sdk::trace::{Sampler, SdkTracerProvider};

use crate::pkg::config::{OtlpProtocol, TelemetryConfig};

/// `service.namespace` всех процессов (docs/observability.md).
pub const SERVICE_NAMESPACE: &str = "nomnom";

/// Пути сигналов OTLP/HTTP относительно `telemetry.endpoint`.
const TRACES_PATH: &str = "v1/traces";
const METRICS_PATH: &str = "v1/metrics";

/// Максимум бакетов base2 exponential histogram (ADR 9): Grafana Cloud считает
/// 0,25 series за непустой бакет, поэтому histogram стоит не больше 10 series.
/// При разбросе длительностей до 2^40 scale не опускается ниже 0, и степени двойки
/// (пороги latency SLO, ADR 40) остаются границами бакетов.
pub const HISTOGRAM_MAX_SIZE: u32 = 40;

/// Максимальный scale base2 exponential histogram — значение SDK по умолчанию;
/// при росте разброса scale уменьшается сам.
pub const HISTOGRAM_MAX_SCALE: i8 = 20;

/// View MeterProvider: каждый histogram instrument агрегируется как base2
/// exponential histogram с [`HISTOGRAM_MAX_SIZE`] и [`HISTOGRAM_MAX_SCALE`];
/// остальные instruments идут view по умолчанию.
#[must_use]
pub fn exponential_histograms(instrument: &Instrument) -> Option<Stream> {
    if instrument.kind() != InstrumentKind::Histogram {
        return None;
    }
    Some(
        Stream::builder()
            .with_aggregation(Aggregation::Base2ExponentialHistogram {
                max_size: HISTOGRAM_MAX_SIZE,
                max_scale: HISTOGRAM_MAX_SCALE,
                record_min_max: true,
            })
            .build()
            .expect("stream without unit and cardinality limit is valid"),
    )
}

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

/// Собирает pipeline процесса и регистрирует глобальные providers и
/// propagator. Вызывается внутри tokio runtime (tonic exporters держат
/// соединение на нём) и до создания instruments: они берутся у глобальных
/// providers при сборке.
pub fn start(config: &TelemetryConfig) -> Result<Telemetry, String> {
    opentelemetry::global::set_text_map_propagator(TraceContextPropagator::new());
    let resource = resource(config);
    let tracer_provider = if config.tracing {
        let provider = SdkTracerProvider::builder()
            .with_batch_exporter(span_exporter(config)?)
            // Значение по умолчанию SDK явно: `OTEL_TRACES_SAMPLER` не действует.
            .with_sampler(Sampler::ParentBased(Box::new(Sampler::AlwaysOn)))
            .with_resource(resource.clone())
            .build();
        opentelemetry::global::set_tracer_provider(provider.clone());
        Some(provider)
    } else {
        None
    };
    let provider = if config.metrics {
        let reader = PeriodicReader::builder(metric_exporter(config)?)
            .with_interval(config.metrics_export_interval)
            .build();
        let provider = SdkMeterProvider::builder()
            .with_reader(reader)
            .with_view(exponential_histograms)
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

fn endpoint(config: &TelemetryConfig) -> Result<&str, String> {
    config
        .endpoint
        .as_deref()
        .ok_or_else(|| "spec.telemetry.endpoint is required to export telemetry".to_owned())
}

/// URL сигнала OTLP/HTTP: `<endpoint>/v1/<signal>`.
fn signal_url(endpoint: &str, path: &str) -> String {
    format!("{}/{path}", endpoint.trim_end_matches('/'))
}

/// Blocking HTTP-клиент OTLP/HTTP (exporters вызывают его из собственных потоков SDK): TLS
/// rustls на ring с корнями webpki, как у клиента Schema Registry, — у reqwest без встроенного
/// провайдера клиент по умолчанию не собирается. Собирается в отдельном потоке: blocking-клиент
/// нельзя создавать внутри tokio runtime.
fn http_client() -> Result<reqwest::blocking::Client, String> {
    std::thread::spawn(|| {
        let tls = rustls::ClientConfig::builder_with_provider(std::sync::Arc::new(
            rustls::crypto::ring::default_provider(),
        ))
        .with_safe_default_protocol_versions()
        .map_err(|error| format!("OTLP http tls: {error}"))?
        .with_root_certificates(rustls::RootCertStore {
            roots: webpki_roots::TLS_SERVER_ROOTS.to_vec(),
        })
        .with_no_client_auth();
        reqwest::blocking::Client::builder()
            .tls_backend_preconfigured(tls)
            .timeout(OTEL_EXPORTER_OTLP_TIMEOUT_DEFAULT)
            .build()
            .map_err(|error| format!("OTLP http client: {error}"))
    })
    .join()
    .map_err(|_| "OTLP http client: build thread panicked".to_owned())?
}

fn span_exporter(config: &TelemetryConfig) -> Result<SpanExporter, String> {
    let endpoint = endpoint(config)?;
    match config.protocol {
        OtlpProtocol::Grpc => SpanExporter::builder()
            .with_tonic()
            .with_protocol(Protocol::Grpc)
            .with_endpoint(endpoint)
            .with_timeout(OTEL_EXPORTER_OTLP_TIMEOUT_DEFAULT)
            .build(),
        OtlpProtocol::HttpProtobuf => SpanExporter::builder()
            .with_http()
            .with_http_client(http_client()?)
            .with_protocol(Protocol::HttpBinary)
            .with_endpoint(signal_url(endpoint, TRACES_PATH))
            .with_timeout(OTEL_EXPORTER_OTLP_TIMEOUT_DEFAULT)
            .build(),
    }
    .map_err(|error| format!("create OTLP span exporter: {error}"))
}

fn metric_exporter(config: &TelemetryConfig) -> Result<MetricExporter, String> {
    let endpoint = endpoint(config)?;
    match config.protocol {
        OtlpProtocol::Grpc => MetricExporter::builder()
            .with_tonic()
            .with_protocol(Protocol::Grpc)
            .with_endpoint(endpoint)
            .with_timeout(OTEL_EXPORTER_OTLP_TIMEOUT_DEFAULT)
            .build(),
        OtlpProtocol::HttpProtobuf => MetricExporter::builder()
            .with_http()
            .with_http_client(http_client()?)
            .with_protocol(Protocol::HttpBinary)
            .with_endpoint(signal_url(endpoint, METRICS_PATH))
            .with_timeout(OTEL_EXPORTER_OTLP_TIMEOUT_DEFAULT)
            .build(),
    }
    .map_err(|error| format!("create OTLP metric exporter: {error}"))
}

/// Resource всех сигналов процесса. Detectors env (`OTEL_SERVICE_NAME`,
/// `OTEL_RESOURCE_ATTRIBUTES`) не подключаются; `service.name` и
/// `service.instance.id` задаются последними и не переопределяются.
#[must_use]
pub fn resource(config: &TelemetryConfig) -> Resource {
    Resource::builder_empty()
        .with_detector(Box::new(TelemetryResourceDetector))
        .with_attribute(KeyValue::new("service.namespace", SERVICE_NAMESPACE))
        .with_attributes(
            config
                .resource_attributes
                .iter()
                .map(|(key, value)| KeyValue::new(key.clone(), value.clone())),
        )
        .with_service_name(config.service_name.clone())
        .with_attribute(KeyValue::new(
            "service.instance.id",
            config.instance_id.clone(),
        ))
        .build()
}

/// Resource логов до загрузки конфигурации: ошибки startup пишутся с
/// `service.name` роли процесса.
#[must_use]
pub fn startup_resource(service_name: &str) -> Resource {
    Resource::builder_empty()
        .with_detector(Box::new(TelemetryResourceDetector))
        .with_attribute(KeyValue::new("service.namespace", SERVICE_NAMESPACE))
        .with_service_name(service_name.to_owned())
        .build()
}

#[cfg(test)]
#[path = "tests.rs"]
mod tests;
