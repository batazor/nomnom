//! Startup-конфигурация клиента: ошибки и endpoint с TLS.

use std::fmt;

use tonic::transport::{ClientTlsConfig, Endpoint};

use crate::pkg::config::SpiceDbConfig;

/// Ошибка startup: невалидная конфигурация SpiceDB.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SpiceDbConfigError(String);

impl fmt::Display for SpiceDbConfigError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "spicedb: {}", self.0)
    }
}

impl std::error::Error for SpiceDbConfigError {}

impl SpiceDbConfigError {
    /// Пустой endpoint: процесс, проверяющий права, не стартует без SpiceDB.
    #[must_use]
    pub fn missing_endpoint(env: &str) -> Self {
        config_error(format!(
            "{env} is required: delivery checks permissions in SpiceDB"
        ))
    }
}

pub(super) fn config_error(message: impl Into<String>) -> SpiceDbConfigError {
    SpiceDbConfigError(message.into())
}

/// `host:port` дополняется схемой по `insecure`; явная схема должна ему
/// соответствовать, чтобы ключ не ушёл plaintext по ошибке.
pub(super) fn endpoint(config: &SpiceDbConfig) -> Result<Endpoint, SpiceDbConfigError> {
    let raw = config.endpoint.trim();
    let url = match raw.split_once("://") {
        None if config.insecure => format!("http://{raw}"),
        None => format!("https://{raw}"),
        Some(("http", _)) if config.insecure => raw.to_owned(),
        Some(("https", _)) if !config.insecure => raw.to_owned(),
        Some((scheme, _)) => {
            return Err(config_error(format!(
                "endpoint scheme {scheme}:// does not match insecure = {}",
                config.insecure
            )));
        }
    };
    let endpoint = Endpoint::from_shared(url)
        .map_err(|error| config_error(format!("invalid endpoint {raw}: {error}")))?
        .connect_timeout(config.timeout)
        .timeout(config.timeout);
    if config.insecure {
        return Ok(endpoint);
    }
    endpoint
        .tls_config(ClientTlsConfig::new().with_webpki_roots())
        .map_err(|error| config_error(format!("tls: {error}")))
}
