//! Конфигурация хранилища процесса: PostgreSQL.

use std::time::Duration;

use config::ConfigError;
use serde::Deserialize;

/// Хранилище `delivery`. URL обязателен для процесса и миграций: пустое
/// значение завершает startup (см. `di::store`). URL приходит из Secret.
///
/// Пул процесса ограничен: размер задаёт Helm (`database.pool.maxConns`, ~10);
/// миграции при старте берут отдельное соединение (`MIGRATE_MAX_CONNS`).
/// Pooler перед PostgreSQL работает только в session mode: миграции держат
/// session-level advisory lock.
#[derive(Clone, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Config {
    /// libpq connection string или URL `postgres://user:password@host/db`.
    pub url: String,
    /// Максимум соединений пула процесса (1..=[`MAX_MAX_CONNS`]).
    pub max_conns: u32,
    /// Минимум простаивающих соединений (0..=[`MAX_MIN_CONNS`]):
    /// idle-соединения не держат серверные соединения session-mode pooler.
    pub min_conns: u32,
    /// Соединение старше закрывается при возврате в пул.
    #[serde(with = "humantime_serde")]
    pub max_lifetime: Duration,
    /// Простаивающее дольше соединение закрывается (выше `min_conns`).
    #[serde(with = "humantime_serde")]
    pub idle_timeout: Duration,
    /// Сколько операция ждёт соединение пула; затем ошибка `PoolTimedOut`
    /// (UNAVAILABLE клиенту) и `db.client.connection.timeouts`.
    #[serde(with = "humantime_serde")]
    pub acquire_timeout: Duration,
}

/// Верхняя граница пула одного процесса.
pub const MAX_MAX_CONNS: u32 = 100;
/// Верхняя граница `min_conns` (session-mode pooler).
pub const MAX_MIN_CONNS: u32 = 1;

impl Default for Config {
    fn default() -> Self {
        Self {
            url: String::new(),
            max_conns: 10,
            min_conns: 0,
            max_lifetime: Duration::from_secs(30 * 60),
            idle_timeout: Duration::from_secs(5 * 60),
            acquire_timeout: Duration::from_secs(5),
        }
    }
}

/// URL содержит пароль и не попадает в логи.
impl std::fmt::Debug for Config {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("Config")
            .field(
                "url",
                &if self.url.is_empty() {
                    ""
                } else {
                    "<redacted>"
                },
            )
            .field("max_conns", &self.max_conns)
            .field("min_conns", &self.min_conns)
            .field("max_lifetime", &self.max_lifetime)
            .field("idle_timeout", &self.idle_timeout)
            .field("acquire_timeout", &self.acquire_timeout)
            .finish()
    }
}

impl Config {
    /// PostgreSQL задан, если задан URL.
    #[must_use]
    pub fn enabled(&self) -> bool {
        !self.url.trim().is_empty()
    }

    pub(super) fn validate(&self) -> Result<(), ConfigError> {
        let invalid = |message: String| Err(ConfigError::Message(message));
        if self.max_conns == 0 || self.max_conns > MAX_MAX_CONNS {
            return invalid(format!(
                "database max_conns must be between 1 and {MAX_MAX_CONNS}, got {}",
                self.max_conns
            ));
        }
        if self.min_conns > MAX_MIN_CONNS {
            return invalid(format!(
                "database min_conns must be between 0 and {MAX_MIN_CONNS}, got {}",
                self.min_conns
            ));
        }
        if self.min_conns > self.max_conns {
            return invalid(format!(
                "database min_conns {} must not exceed max_conns {}",
                self.min_conns, self.max_conns
            ));
        }
        for (name, value) in [
            ("max_lifetime", self.max_lifetime),
            ("idle_timeout", self.idle_timeout),
            ("acquire_timeout", self.acquire_timeout),
        ] {
            if value.is_zero() {
                return invalid(format!("database {name} must be positive"));
            }
        }
        if self.idle_timeout > self.max_lifetime {
            return invalid(format!(
                "database idle_timeout {:?} must not exceed max_lifetime {:?}",
                self.idle_timeout, self.max_lifetime
            ));
        }
        Ok(())
    }
}
