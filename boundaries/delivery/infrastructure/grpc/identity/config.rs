//! Конфигурация identity: чтение JWKS из файлов, разбор allowlist
//! ServiceAccount и делегатов, выбор verifier'ов по [`IdentityConfig`].
//! Невалидная конфигурация — ошибка startup.

use std::collections::{HashMap, HashSet};
use std::fmt;

use crate::pkg::config::IdentityConfig;

use super::jwks::parse_user_jwks;
use super::service_token::ServiceTokenVerifier;
use super::user_assertion::UserAssertionVerifier;

const SUBJECT_PREFIX: &str = "system:serviceaccount:";

/// Ошибка startup: невалидная конфигурация identity.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct IdentityConfigError(pub(super) String);

impl fmt::Display for IdentityConfigError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "identity: {}", self.0)
    }
}

impl std::error::Error for IdentityConfigError {}

pub(super) fn config_error(message: impl Into<String>) -> IdentityConfigError {
    IdentityConfigError(message.into())
}

impl ServiceTokenVerifier {
    /// `Ok(None)`, если проверка не настроена (пусты issuer, audience или
    /// путь к JWKS). Нечитаемый JWKS и невалидный allowlist — ошибка.
    pub(super) fn from_config(
        config: &IdentityConfig,
    ) -> Result<Option<Self>, IdentityConfigError> {
        let issuer = config.issuer.trim();
        let audience = config.audience.trim();
        let jwks_file = config.jwks_file.trim();
        if issuer.is_empty() || audience.is_empty() || jwks_file.is_empty() {
            return Ok(None);
        }
        let jwks = std::fs::read_to_string(jwks_file)
            .map_err(|error| config_error(format!("read JWKS {jwks_file}: {error}")))?;
        Self::new(issuer, audience, &jwks, &config.service_accounts).map(Some)
    }
}

impl UserAssertionVerifier {
    /// `Ok(None)`, если не заданы user issuer, JWKS BFF, делегаты или
    /// audience. Нечитаемый JWKS и невалидный список делегатов — ошибка.
    pub fn from_config(config: &IdentityConfig) -> Result<Option<Self>, IdentityConfigError> {
        let issuer = config.user_issuer.trim();
        let jwks_file = config.user_jwks_file.trim();
        let delegates = config.user_delegates.trim();
        let audience = config.audience.trim();
        if issuer.is_empty() || jwks_file.is_empty() || delegates.is_empty() {
            if !(issuer.is_empty() && jwks_file.is_empty() && delegates.is_empty()) {
                eprintln!(
                    "delivery: warning: IDENTITY__USER_ISSUER, IDENTITY__USER_JWKS_FILE and \
                     IDENTITY__USER_DELEGATES must all be set; every call with a user assertion \
                     is rejected as unauthenticated"
                );
            }
            return Ok(None);
        }
        let jwks = std::fs::read_to_string(jwks_file)
            .map_err(|error| config_error(format!("read BFF JWKS {jwks_file}: {error}")))?;
        if audience.is_empty() {
            // Service identity выключена, и вызовы всё равно отклоняются;
            // JWKS и делегаты проверяются, чтобы ошибка не ждала audience.
            parse_user_jwks(&jwks)?;
            parse_delegates(delegates)?;
            return Ok(None);
        }
        Self::new(issuer, audience, &jwks, delegates).map(Some)
    }
}

pub(super) fn parse_delegates(value: &str) -> Result<HashSet<String>, IdentityConfigError> {
    let mut delegates = HashSet::new();
    for entry in value.split(',') {
        let entry = entry.trim();
        if entry.is_empty()
            || entry.contains(['/', '=', ':'])
            || entry.contains(char::is_whitespace)
        {
            return Err(config_error(format!(
                "malformed IDENTITY__USER_DELEGATES entry {entry:?}, expected service-name"
            )));
        }
        delegates.insert(entry.to_owned());
    }
    Ok(delegates)
}

pub(super) fn parse_service_accounts(
    value: &str,
) -> Result<HashMap<String, String>, IdentityConfigError> {
    let mut accounts = HashMap::new();
    if value.trim().is_empty() {
        return Err(config_error(
            "IDENTITY__SERVICE_ACCOUNTS must not be empty when IDENTITY__ISSUER is set",
        ));
    }
    for entry in value.split(',') {
        let entry = entry.trim();
        let malformed = || {
            config_error(format!(
                "malformed service account {entry:?}, expected namespace/serviceaccount=service-name"
            ))
        };
        let (account, service) = entry.split_once('=').ok_or_else(malformed)?;
        let (namespace, name) = account.trim().split_once('/').ok_or_else(malformed)?;
        let (namespace, name, service) = (namespace.trim(), name.trim(), service.trim());
        let valid = |part: &str| {
            !part.is_empty()
                && !part.contains(['/', '=', ':', ','])
                && !part.contains(char::is_whitespace)
        };
        if !(valid(namespace) && valid(name) && valid(service)) {
            return Err(malformed());
        }
        let subject = format!("{SUBJECT_PREFIX}{namespace}:{name}");
        if accounts.insert(subject, service.to_owned()).is_some() {
            return Err(config_error(format!(
                "duplicate service account {namespace}/{name}"
            )));
        }
    }
    Ok(accounts)
}
