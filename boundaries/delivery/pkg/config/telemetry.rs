//! OpenTelemetry процесса (ADR 9), секции `spec.telemetry`, `spec.tracing`,
//! `spec.metrics` и `spec.logging`. Процесс передаёт SDK endpoint, протокол,
//! resource и интервал явно; `OTEL_*` и `LOG__LEVEL` не читаются.
//!
//! - `telemetry.endpoint` — URL OTLP коллектора, значения по умолчанию нет;
//!   обязателен, если включён экспорт traces или metrics.
//! - `telemetry.protocol` — `grpc` (по умолчанию) или `http/protobuf`.
//! - `telemetry.serviceName` — `service.name`, по умолчанию роль процесса.
//! - `telemetry.resourceAttributes` — атрибуты resource; `service.name` и
//!   `service.instance.id` в них запрещены.
//! - `tracing.enabled`, `metrics.enabled` — экспорт сигнала, по умолчанию
//!   выключен: endpoint без флагов ничего не экспортирует.
//! - `metrics.exportInterval` — больше нуля, по умолчанию `60s`.
//! - `logging.level` — `debug`, `info` (по умолчанию), `warn`, `error`.
//!
//! `service.instance.id` — имя pod из env `POD_NAME` (Downward API),
//! обязательно.

use std::collections::BTreeMap;
use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

/// Имя pod (Downward API) — `service.instance.id`.
pub const POD_NAME_ENV: &str = "POD_NAME";
/// Интервал экспорта metrics по умолчанию, как у OpenTelemetry SDK.
pub const DEFAULT_METRICS_EXPORT_INTERVAL: Duration = Duration::from_secs(60);

/// Атрибуты resource, которые задаёт сам процесс.
const RESERVED_RESOURCE_ATTRIBUTES: [(&str, &str); 2] = [
    ("service.name", "spec.telemetry.serviceName"),
    ("service.instance.id", "env POD_NAME"),
];

/// Протокол OTLP exporters.
#[derive(Clone, Copy, Debug, Default, Deserialize, PartialEq, Eq)]
#[serde(try_from = "String")]
pub enum OtlpProtocol {
    #[default]
    Grpc,
    HttpProtobuf,
}

impl TryFrom<String> for OtlpProtocol {
    type Error = String;

    fn try_from(value: String) -> Result<Self, Self::Error> {
        match value.as_str() {
            "grpc" => Ok(Self::Grpc),
            "http/protobuf" => Ok(Self::HttpProtobuf),
            other => Err(format!(
                "unknown OTLP protocol {other:?}, expected grpc or http/protobuf"
            )),
        }
    }
}

/// Уровень логов кода процесса.
#[derive(Clone, Copy, Debug, Default, Deserialize, PartialEq, Eq)]
#[serde(try_from = "String")]
pub enum LogLevel {
    Debug,
    #[default]
    Info,
    Warn,
    Error,
}

impl TryFrom<String> for LogLevel {
    type Error = String;

    fn try_from(value: String) -> Result<Self, String> {
        match value.as_str() {
            "debug" => Ok(Self::Debug),
            "info" => Ok(Self::Info),
            "warn" => Ok(Self::Warn),
            "error" => Ok(Self::Error),
            other => Err(format!(
                "unknown log level {other:?}, expected debug, info, warn or error"
            )),
        }
    }
}

/// Секция `spec.telemetry`.
#[derive(Debug, Default, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase", default)]
pub(super) struct TelemetrySpec {
    endpoint: Option<String>,
    protocol: OtlpProtocol,
    service_name: Option<String>,
    resource_attributes: BTreeMap<String, String>,
}

/// Секция `spec.tracing`.
#[derive(Debug, Default, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase", default)]
pub(super) struct TracingSpec {
    enabled: bool,
}

/// Секция `spec.metrics`.
#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase", default)]
pub(super) struct MetricsSpec {
    enabled: bool,
    #[serde(with = "humantime_serde")]
    export_interval: Duration,
}

impl Default for MetricsSpec {
    fn default() -> Self {
        Self {
            enabled: false,
            export_interval: DEFAULT_METRICS_EXPORT_INTERVAL,
        }
    }
}

/// Секция `spec.logging`.
#[derive(Debug, Default, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase", default)]
pub(super) struct LoggingSpec {
    level: LogLevel,
}

/// Секции телеметрии файла.
#[derive(Debug, Default)]
pub(super) struct TelemetrySections {
    pub telemetry: TelemetrySpec,
    pub tracing: TracingSpec,
    pub metrics: MetricsSpec,
    pub logging: LoggingSpec,
}

/// Проверенная конфигурация телеметрии процесса.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TelemetryConfig {
    /// `service.name`.
    pub service_name: String,
    /// `service.instance.id` — имя pod.
    pub instance_id: String,
    /// URL OTLP коллектора; задан, если включён экспорт хотя бы одного сигнала.
    pub endpoint: Option<String>,
    pub protocol: OtlpProtocol,
    /// Атрибуты resource окружения (`deployment.environment.name`,
    /// `service.version` и т. п.).
    pub resource_attributes: BTreeMap<String, String>,
    /// Экспорт spans.
    pub tracing: bool,
    /// Экспорт metrics.
    pub metrics: bool,
    pub metrics_export_interval: Duration,
    pub log_level: LogLevel,
}

impl TelemetrySections {
    /// Конфигурация телеметрии процесса с ролью `role` (`service.name` по
    /// умолчанию) и именем pod `pod_name`.
    pub(super) fn resolve(
        self,
        role: &str,
        pod_name: Option<String>,
    ) -> Result<TelemetryConfig, ConfigError> {
        let invalid = |message: String| Err(ConfigError::Message(message));
        let Some(instance_id) = pod_name.filter(|name| !name.trim().is_empty()) else {
            return invalid(format!("{POD_NAME_ENV} is required (service.instance.id)"));
        };
        let TelemetrySpec {
            endpoint,
            protocol,
            service_name,
            resource_attributes,
        } = self.telemetry;
        let service_name = match service_name {
            None => role.to_owned(),
            Some(name) if name.trim().is_empty() => {
                return invalid("spec.telemetry.serviceName must not be empty".to_owned());
            }
            Some(name) => name,
        };
        for (attribute, source) in RESERVED_RESOURCE_ATTRIBUTES {
            if resource_attributes.contains_key(attribute) {
                return invalid(format!(
                    "spec.telemetry.resourceAttributes must not set {attribute}: it comes from {source}"
                ));
            }
        }
        let endpoint = endpoint.filter(|endpoint| !endpoint.trim().is_empty());
        if (self.tracing.enabled || self.metrics.enabled) && endpoint.is_none() {
            return invalid(
                "spec.telemetry.endpoint is required when spec.tracing.enabled or \
                 spec.metrics.enabled is true"
                    .to_owned(),
            );
        }
        if self.metrics.export_interval.is_zero() {
            return invalid("spec.metrics.exportInterval must be positive".to_owned());
        }
        Ok(TelemetryConfig {
            service_name,
            instance_id,
            endpoint,
            protocol,
            resource_attributes,
            tracing: self.tracing.enabled,
            metrics: self.metrics.enabled,
            metrics_export_interval: self.metrics.export_interval,
            log_level: self.logging.level,
        })
    }
}
