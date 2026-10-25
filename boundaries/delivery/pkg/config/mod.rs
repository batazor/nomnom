use std::path::Path;
use std::time::Duration;

use config::{Config as ConfigLoader, ConfigError, File};
use config::{ConfigBuilder, builder::DefaultState};
use serde::Deserialize;

const CONFIG_KEY_PORT: &str = "server.port";
pub const DEFAULT_PORT: u16 = 8083;
pub const PORT_ENV: &str = "SERVER__PORT";

const CONFIG_KEY_MANAGEMENT_PORT: &str = "management.port";
pub const DEFAULT_MANAGEMENT_PORT: u16 = 9083;
pub const MANAGEMENT_PORT_ENV: &str = "MANAGEMENT__PORT";

const CONFIG_KEY_DRAIN_TIMEOUT: &str = "shutdown.drain_timeout";
pub const DEFAULT_DRAIN_TIMEOUT: &str = "20s";
pub const DRAIN_TIMEOUT_ENV: &str = "SHUTDOWN__DRAIN_TIMEOUT";

const CONFIG_KEY_READINESS_DELAY: &str = "shutdown.readiness_delay";
pub const DEFAULT_READINESS_DELAY: &str = "5s";
pub const READINESS_DELAY_ENV: &str = "SHUTDOWN__READINESS_DELAY";

/// Service identity (ADR 27): ключ конфигурации и переменная окружения.
const IDENTITY_SETTINGS: [(&str, &str); 4] = [
    ("identity.issuer", ISSUER_ENV),
    ("identity.audience", AUDIENCE_ENV),
    ("identity.jwks_file", JWKS_FILE_ENV),
    ("identity.service_accounts", SERVICE_ACCOUNTS_ENV),
];
pub const ISSUER_ENV: &str = "IDENTITY__ISSUER";
pub const AUDIENCE_ENV: &str = "IDENTITY__AUDIENCE";
pub const JWKS_FILE_ENV: &str = "IDENTITY__JWKS_FILE";
pub const SERVICE_ACCOUNTS_ENV: &str = "IDENTITY__SERVICE_ACCOUNTS";

#[derive(Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ServerConfig {
    pub port: u16,
}

/// Технический порт (Kubernetes-имя `metrics`): health и metrics отделены
/// от бизнес-порта.
#[derive(Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ManagementConfig {
    pub port: u16,
}

#[derive(Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ShutdownConfig {
    /// Ожидание распространения not ready до остановки приёма работы.
    /// Нулевое значение допустимо для процессов без входящего трафика.
    #[serde(with = "humantime_serde")]
    pub readiness_delay: Duration,
    /// Deadline на завершение начатой работы после `SIGTERM`/`SIGINT`.
    #[serde(with = "humantime_serde")]
    pub drain_timeout: Duration,
}

/// Проверка projected ServiceAccount token (ADR 27). Пустые issuer, audience
/// или jwks_file означают, что проверка выключена и вызовы отклоняются.
#[derive(Debug, Default, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct IdentityConfig {
    /// Ожидаемый `iss` token (issuer кластера).
    pub issuer: String,
    /// Audience контекста, которую должен содержать `aud`.
    pub audience: String,
    /// Путь к JWKS кластера (`/openid/v1/jwks`).
    pub jwks_file: String,
    /// Allowlist `namespace/serviceaccount=service-name` через запятую.
    pub service_accounts: String,
}

#[derive(Debug, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct AppConfig {
    pub server: ServerConfig,
    pub management: ManagementConfig,
    pub shutdown: ShutdownConfig,
    pub identity: IdentityConfig,
}

pub fn load() -> Result<AppConfig, ConfigError> {
    let config_file = std::env::var("CONFIG_FILE").ok();
    load_from(config_file.as_deref().map(Path::new))
}

fn load_from(config_file: Option<&Path>) -> Result<AppConfig, ConfigError> {
    let mut builder = ConfigLoader::builder();

    if let Some(path) = config_file {
        builder = builder.add_source(File::from(path).required(true));
    }

    builder = configure_server(builder)?;
    builder = configure_management(builder)?;
    builder = configure_shutdown(builder)?;
    builder = configure_identity(builder, |name| std::env::var(name).ok())?;

    let config: AppConfig = builder.build()?.try_deserialize()?;
    config.server.validate()?;
    config.management.validate(&config.server)?;
    config.shutdown.validate()?;

    Ok(config)
}

fn configure_server(
    mut builder: ConfigBuilder<DefaultState>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    builder = builder.set_default(CONFIG_KEY_PORT, DEFAULT_PORT)?;

    if let Ok(port) = std::env::var(PORT_ENV).or_else(|_| std::env::var("PORT")) {
        builder = builder.set_override(CONFIG_KEY_PORT, port)?;
    }

    Ok(builder)
}

fn configure_management(
    mut builder: ConfigBuilder<DefaultState>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    builder = builder.set_default(CONFIG_KEY_MANAGEMENT_PORT, DEFAULT_MANAGEMENT_PORT)?;

    if let Ok(port) = std::env::var(MANAGEMENT_PORT_ENV) {
        builder = builder.set_override(CONFIG_KEY_MANAGEMENT_PORT, port)?;
    }

    Ok(builder)
}

fn configure_shutdown(
    mut builder: ConfigBuilder<DefaultState>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    builder = builder.set_default(CONFIG_KEY_DRAIN_TIMEOUT, DEFAULT_DRAIN_TIMEOUT)?;

    if let Ok(timeout) = std::env::var(DRAIN_TIMEOUT_ENV) {
        builder = builder.set_override(CONFIG_KEY_DRAIN_TIMEOUT, timeout)?;
    }

    builder = builder.set_default(CONFIG_KEY_READINESS_DELAY, DEFAULT_READINESS_DELAY)?;

    if let Ok(delay) = std::env::var(READINESS_DELAY_ENV) {
        builder = builder.set_override(CONFIG_KEY_READINESS_DELAY, delay)?;
    }

    Ok(builder)
}

fn configure_identity(
    mut builder: ConfigBuilder<DefaultState>,
    env: impl Fn(&str) -> Option<String>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    for (key, name) in IDENTITY_SETTINGS {
        builder = builder.set_default(key, "")?;
        if let Some(value) = env(name) {
            builder = builder.set_override(key, value)?;
        }
    }
    Ok(builder)
}

impl ShutdownConfig {
    fn validate(&self) -> Result<(), ConfigError> {
        if self.drain_timeout.is_zero() {
            return Err(ConfigError::Message(
                "shutdown drain timeout must be positive".to_owned(),
            ));
        }
        Ok(())
    }
}

impl ManagementConfig {
    fn validate(&self, server: &ServerConfig) -> Result<(), ConfigError> {
        if self.port == 0 {
            return Err(ConfigError::Message(
                "management port must be between 1 and 65535".to_owned(),
            ));
        }
        if self.port == server.port {
            return Err(ConfigError::Message(format!(
                "management port must differ from server port {}",
                server.port
            )));
        }
        Ok(())
    }
}

impl ServerConfig {
    fn validate(&self) -> Result<(), ConfigError> {
        if self.port == 0 {
            return Err(ConfigError::Message(
                "server port must be between 1 and 65535".to_owned(),
            ));
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;
    use std::time::{SystemTime, UNIX_EPOCH};

    #[test]
    fn loads_port_from_toml() {
        let name = format!(
            "nomnom-delivery-config-{}-{}.toml",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .expect("clock before epoch")
                .as_nanos()
        );
        let path = std::env::temp_dir().join(name);
        fs::write(&path, "[server]\nport = 9003\n").expect("write config");

        let config = load_from(Some(&path)).expect("load config");
        fs::remove_file(path).expect("remove config");

        assert_eq!(config.server.port, 9003);
        assert_eq!(config.management.port, DEFAULT_MANAGEMENT_PORT);
        assert_eq!(config.shutdown.drain_timeout, Duration::from_secs(20));
        assert_eq!(config.shutdown.readiness_delay, Duration::from_secs(5));
    }

    /// Пишет TOML во временный файл с уникальным именем и загружает его.
    fn load_toml(name: &str, contents: &str) -> Result<AppConfig, ConfigError> {
        let path = std::env::temp_dir().join(format!(
            "nomnom-delivery-{name}-{}.toml",
            std::process::id()
        ));
        fs::write(&path, contents).expect("write config");
        let result = load_from(Some(&path));
        fs::remove_file(path).expect("remove config");
        result
    }

    #[test]
    fn loads_management_port_from_toml() {
        let config = load_toml("management", "[management]\nport = 9100\n").expect("load config");

        assert_eq!(config.management.port, 9100);
    }

    #[test]
    fn rejects_zero_management_port() {
        assert!(load_toml("zero-management", "[management]\nport = 0\n").is_err());
    }

    #[test]
    fn rejects_management_port_equal_to_server_port() {
        let result = load_toml(
            "same-ports",
            "[server]\nport = 9200\n\n[management]\nport = 9200\n",
        );

        assert!(result.is_err());
    }

    #[test]
    fn loads_readiness_delay_from_toml() {
        let config = load_toml("readiness", "[shutdown]\nreadiness_delay = \"500ms\"\n")
            .expect("load config");

        assert_eq!(config.shutdown.readiness_delay, Duration::from_millis(500));
    }

    #[test]
    fn allows_zero_readiness_delay() {
        let config = load_toml("zero-readiness", "[shutdown]\nreadiness_delay = \"0s\"\n")
            .expect("load config");

        assert_eq!(config.shutdown.readiness_delay, Duration::ZERO);
    }

    #[test]
    fn rejects_invalid_readiness_delay() {
        assert!(load_toml("bad-readiness", "[shutdown]\nreadiness_delay = \"soon\"\n").is_err());
    }

    #[test]
    fn loads_drain_timeout_from_toml() {
        let name = format!("nomnom-delivery-drain-{}.toml", std::process::id());
        let path = std::env::temp_dir().join(name);
        fs::write(&path, "[shutdown]\ndrain_timeout = \"1500ms\"\n").expect("write config");

        let config = load_from(Some(&path)).expect("load config");
        fs::remove_file(path).expect("remove config");

        assert_eq!(config.shutdown.drain_timeout, Duration::from_millis(1500));
    }

    #[test]
    fn rejects_zero_drain_timeout() {
        let name = format!("nomnom-delivery-zero-drain-{}.toml", std::process::id());
        let path = std::env::temp_dir().join(name);
        fs::write(&path, "[shutdown]\ndrain_timeout = \"0s\"\n").expect("write config");

        let result = load_from(Some(&path));
        fs::remove_file(path).expect("remove config");

        assert!(result.is_err());
    }

    #[test]
    fn identity_is_disabled_by_default() {
        let config = load_toml("identity-default", "").expect("load config");

        assert_eq!(config.identity, IdentityConfig::default());
    }

    #[test]
    fn loads_identity_from_toml() {
        let config = load_toml(
            "identity",
            "[identity]\nissuer = \"https://issuer\"\naudience = \"nomnom.delivery\"\n\
             jwks_file = \"/etc/jwks.json\"\nservice_accounts = \"nomnom/orders-worker=orders-worker\"\n",
        )
        .expect("load config");

        assert_eq!(config.identity.issuer, "https://issuer");
        assert_eq!(config.identity.audience, "nomnom.delivery");
        assert_eq!(config.identity.jwks_file, "/etc/jwks.json");
        assert_eq!(
            config.identity.service_accounts,
            "nomnom/orders-worker=orders-worker"
        );
    }

    #[test]
    fn identity_env_overrides_file_values() {
        let env = std::collections::HashMap::from([
            (ISSUER_ENV, "https://env-issuer"),
            (AUDIENCE_ENV, "nomnom.delivery"),
            (JWKS_FILE_ENV, "/etc/nomnom/identity/jwks.json"),
            (SERVICE_ACCOUNTS_ENV, "nomnom/orders-worker=orders-worker"),
        ]);
        let builder = ConfigLoader::builder()
            .set_default("identity.issuer", "https://file-issuer")
            .expect("default");
        let identity: IdentityConfig = configure_identity(builder, |name| {
            env.get(name).map(|value| (*value).to_owned())
        })
        .expect("configure")
        .build()
        .expect("build")
        .get("identity")
        .expect("identity section");

        assert_eq!(
            identity,
            IdentityConfig {
                issuer: "https://env-issuer".to_owned(),
                audience: "nomnom.delivery".to_owned(),
                jwks_file: "/etc/nomnom/identity/jwks.json".to_owned(),
                service_accounts: "nomnom/orders-worker=orders-worker".to_owned(),
            }
        );
    }

    #[test]
    fn rejects_zero_port() {
        let name = format!("nomnom-delivery-invalid-{}.toml", std::process::id());
        let path = std::env::temp_dir().join(name);
        fs::write(&path, "[server]\nport = 0\n").expect("write config");

        let result = load_from(Some(&path));
        fs::remove_file(path).expect("remove config");

        assert!(result.is_err());
    }
}
