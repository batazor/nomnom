//! Service identity по projected ServiceAccount token (ADR 27).
//!
//! [`ServiceTokenSubjectResolver`] проверяет `authorization: Bearer <jwt>` по
//! JWKS кластера и переводит ServiceAccount из allowlist в
//! [`Subject::Service`]. Любой отказ — `UNAUTHENTICATED` с общим сообщением;
//! причина пишется только в log.
//!
//! User identity от BFF (ADR 32): после service token resolver смотрит
//! metadata `x-nomnom-user-assertion`. Без неё вызов остаётся
//! [`Subject::Service`]; с ней вызывающий service должен входить в
//! `IDENTITY__USER_DELEGATES`, а assertion — пройти
//! [`UserAssertionVerifier`]. Результат — [`Subject::User`], service identity
//! делегата остаётся в [`Caller::delegate`] для log.
//!
//! Модули: `config` — чтение JWKS и allowlist из [`IdentityConfig`], `jwks`
//! — разбор ключей, `service_token` и `user_assertion` — verifier'ы; resolver
//! только соединяет их в порядке ADR 32.

mod config;
mod jwks;
mod service_token;
mod user_assertion;

use std::sync::Arc;

use tonic::Status;
use tonic::metadata::MetadataMap;

use crate::pkg::application::Subject;
use crate::pkg::config::IdentityConfig;

use super::service::SubjectResolver;
use service_token::ServiceTokenVerifier;

pub use config::IdentityConfigError;
pub use user_assertion::{Clock, MAX_USER_ASSERTION_TTL_SECONDS, UserAssertionVerifier};

#[cfg(test)]
use service_token::AUTHORIZATION;

/// Допуск на расхождение часов для `exp` и `nbf`.
pub const CLOCK_LEEWAY_SECONDS: u64 = 30;

const USER_ASSERTION: &str = "x-nomnom-user-assertion";
const UNAUTHENTICATED: &str = "verified caller identity is required";

/// Resolver процесса: проверка service token; без issuer, audience или JWKS
/// процесс не стартует.
pub fn subject_resolver(
    config: &IdentityConfig,
) -> Result<Arc<dyn SubjectResolver>, IdentityConfigError> {
    Ok(Arc::new(ServiceTokenSubjectResolver::from_config(config)?))
}

pub struct ServiceTokenSubjectResolver {
    services: ServiceTokenVerifier,
    /// `None` — user assertions не настроены и отклоняются.
    users: Option<UserAssertionVerifier>,
}

/// Аутентифицированный вызывающий: subject для use cases и, если subject
/// пришёл из user assertion, service identity делегата для log и audit.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Caller {
    pub subject: Subject,
    pub delegate: Option<String>,
}

impl ServiceTokenSubjectResolver {
    /// `Ok(None)`, если проверка не настроена (пусты issuer, audience или
    /// путь к JWKS). Нечитаемый JWKS и невалидный allowlist — ошибка.
    pub fn from_config(config: &IdentityConfig) -> Result<Self, IdentityConfigError> {
        let services = ServiceTokenVerifier::from_config(config)?;
        Ok(Self {
            services,
            users: UserAssertionVerifier::from_config(config)?,
        })
    }

    pub fn new(
        issuer: &str,
        audience: &str,
        jwks: &str,
        service_accounts: &str,
    ) -> Result<Self, IdentityConfigError> {
        Ok(Self {
            services: ServiceTokenVerifier::new(issuer, audience, jwks, service_accounts)?,
            users: None,
        })
    }

    /// Включает приём user assertions; `None` оставляет их отклонение.
    pub fn with_user_assertions(mut self, users: Option<UserAssertionVerifier>) -> Self {
        self.users = users;
        self
    }

    /// Порядок ADR 32: service token, затем необязательный user assertion.
    /// Ошибка — причина отказа для log, без token.
    pub fn authenticate(&self, metadata: &MetadataMap) -> Result<Caller, String> {
        let service = self.services.verify(metadata)?;
        let mut assertions = metadata.get_all(USER_ASSERTION).iter();
        let Some(assertion) = assertions.next() else {
            return Ok(Caller {
                subject: Subject::Service(service),
                delegate: None,
            });
        };
        if assertions.next().is_some() {
            return Err(format!("repeated {USER_ASSERTION} from {service:?}"));
        }
        let token = assertion
            .to_str()
            .map_err(|_| format!("{USER_ASSERTION} from {service:?} is not ASCII"))?
            .trim();
        if token.is_empty() {
            return Err(format!("empty {USER_ASSERTION} from {service:?}"));
        }
        let users = self.users.as_ref().ok_or_else(|| {
            format!("user assertion from {service:?} rejected: user assertions are not configured")
        })?;
        let user = users.verify(&service, token)?;
        tracing::info!(
            operation = "identity.user_assertion",
            outcome = "accepted",
            jti = ?user.jti,
            delegate = %service,
            "user assertion accepted"
        );
        Ok(Caller {
            subject: Subject::User(user.sub),
            delegate: Some(service),
        })
    }
}

impl SubjectResolver for ServiceTokenSubjectResolver {
    fn resolve(&self, metadata: &MetadataMap) -> Result<Subject, Status> {
        self.authenticate(metadata)
            .map(|caller| caller.subject)
            .map_err(|reason| {
                tracing::warn!(operation = "identity.verify", outcome = "rejected", error.type = "unauthenticated", error.message = %reason, "unauthenticated gRPC call");
                Status::unauthenticated(UNAUTHENTICATED)
            })
    }
}

/// Общие тестовые векторы `tests/identity` (AC-461).
#[cfg(test)]
pub(crate) mod fixtures;

#[cfg(test)]
mod tests;
