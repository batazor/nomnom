//! Загрузка [`AppConfig`]: файл, значения по умолчанию, переменные окружения
//! и проверка.

use std::path::Path;

use config::{Config as ConfigLoader, ConfigError, File};
use config::{ConfigBuilder, builder::DefaultState};

use super::simulator::SIMULATOR_SETTINGS;
use super::{
    AppConfig, CONFIG_KEY_DRAIN_TIMEOUT, CONFIG_KEY_MANAGEMENT_PORT, CONFIG_KEY_PORT,
    CONFIG_KEY_READINESS_DELAY, DATABASE_SETTINGS, DEFAULT_DRAIN_TIMEOUT, DEFAULT_MANAGEMENT_PORT,
    DEFAULT_PORT, DEFAULT_READINESS_DELAY, DRAIN_TIMEOUT_ENV, IDENTITY_SETTINGS,
    MANAGEMENT_PORT_ENV, OFFERS_SETTINGS, PORT_ENV, READINESS_DELAY_ENV, RECONCILER_SETTINGS,
    SPICEDB_SETTINGS,
};

pub fn load() -> Result<AppConfig, ConfigError> {
    let config_file = std::env::var("CONFIG_FILE").ok();
    load_from(config_file.as_deref().map(Path::new))
}

pub(super) fn load_from(config_file: Option<&Path>) -> Result<AppConfig, ConfigError> {
    let mut builder = ConfigLoader::builder();

    if let Some(path) = config_file {
        builder = builder.add_source(File::from(path).required(true));
    }

    builder = configure_server(builder)?;
    builder = configure_management(builder)?;
    builder = configure_shutdown(builder)?;
    builder = configure_identity(builder, |name| std::env::var(name).ok())?;
    builder = configure_spicedb(builder, |name| std::env::var(name).ok())?;
    builder = configure_reconciler(builder, |name| std::env::var(name).ok())?;
    builder = configure_settings(builder, &OFFERS_SETTINGS, |name| std::env::var(name).ok())?;
    builder = configure_database(builder, |name| std::env::var(name).ok())?;
    builder = configure_simulator(builder, |name| std::env::var(name).ok())?;

    let config: AppConfig = builder.build()?.try_deserialize()?;
    config.server.validate()?;
    config.management.validate(&config.server)?;
    config.shutdown.validate()?;
    config.spicedb.validate()?;
    config.reconciler.validate()?;
    config.offers.validate()?;
    config.database.validate()?;
    config.simulator.validate()?;

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

pub(super) fn configure_identity(
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

pub(super) fn configure_spicedb(
    mut builder: ConfigBuilder<DefaultState>,
    env: impl Fn(&str) -> Option<String>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    for (key, name, default) in SPICEDB_SETTINGS {
        builder = builder.set_default(key, default)?;
        if let Some(value) = env(name) {
            builder = builder.set_override(key, value)?;
        }
    }
    Ok(builder)
}

fn configure_database(
    mut builder: ConfigBuilder<DefaultState>,
    env: impl Fn(&str) -> Option<String>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    for (key, name, default) in DATABASE_SETTINGS {
        builder = builder.set_default(key, default)?;
        if let Some(value) = env(name) {
            builder = builder.set_override(key, value)?;
        }
    }
    Ok(builder)
}

pub(super) fn configure_settings(
    mut builder: ConfigBuilder<DefaultState>,
    settings: &[(&str, &str, &str)],
    env: impl Fn(&str) -> Option<String>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    for (key, name, default) in settings {
        builder = builder.set_default(*key, *default)?;
        if let Some(value) = env(name) {
            builder = builder.set_override(*key, value)?;
        }
    }
    Ok(builder)
}

pub(super) fn configure_reconciler(
    mut builder: ConfigBuilder<DefaultState>,
    env: impl Fn(&str) -> Option<String>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    for (key, name, default) in RECONCILER_SETTINGS {
        builder = builder.set_default(key, default)?;
        if let Some(value) = env(name) {
            builder = builder.set_override(key, value)?;
        }
    }
    Ok(builder)
}

/// Simulator без значений по умолчанию: ключ задаёт файл или переменная.
pub(super) fn configure_simulator(
    mut builder: ConfigBuilder<DefaultState>,
    env: impl Fn(&str) -> Option<String>,
) -> Result<ConfigBuilder<DefaultState>, ConfigError> {
    for (key, name) in SIMULATOR_SETTINGS {
        if let Some(value) = env(name) {
            builder = builder.set_override(key, value)?;
        }
    }
    Ok(builder)
}
