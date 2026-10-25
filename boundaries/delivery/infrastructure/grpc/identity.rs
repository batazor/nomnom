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

use std::collections::{HashMap, HashSet};
use std::fmt;
use std::sync::Arc;

use jsonwebtoken::jwk::{AlgorithmParameters, EllipticCurve, Jwk, JwkSet, KeyAlgorithm};
use jsonwebtoken::{Algorithm, DecodingKey, Validation, decode, decode_header};
use serde::Deserialize;
use tonic::Status;
use tonic::metadata::MetadataMap;

use crate::pkg::application::Subject;
use crate::pkg::config::IdentityConfig;

use super::service::{RejectingSubjectResolver, SubjectResolver};

/// Допуск на расхождение часов для `exp` и `nbf`.
pub const CLOCK_LEEWAY_SECONDS: u64 = 30;

/// Максимальный `exp - iat` user assertion (ADR 32).
pub const MAX_USER_ASSERTION_TTL_SECONDS: u64 = 60;

const AUTHORIZATION: &str = "authorization";
const USER_ASSERTION: &str = "x-nomnom-user-assertion";
const BEARER: &str = "Bearer ";
const SUBJECT_PREFIX: &str = "system:serviceaccount:";
const UNAUTHENTICATED: &str = "verified caller identity is required";

/// Ошибка startup: невалидная конфигурация identity.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct IdentityConfigError(String);

impl fmt::Display for IdentityConfigError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "identity: {}", self.0)
    }
}

impl std::error::Error for IdentityConfigError {}

fn config_error(message: impl Into<String>) -> IdentityConfigError {
    IdentityConfigError(message.into())
}

/// Выбирает resolver процесса: без issuer, audience или JWKS — fail closed
/// через [`RejectingSubjectResolver`], иначе проверка service token.
pub fn subject_resolver(
    config: &IdentityConfig,
) -> Result<Arc<dyn SubjectResolver>, IdentityConfigError> {
    match ServiceTokenSubjectResolver::from_config(config)? {
        Some(resolver) => Ok(Arc::new(resolver)),
        None => {
            eprintln!(
                "delivery: warning: IDENTITY__ISSUER, IDENTITY__AUDIENCE or IDENTITY__JWKS_FILE \
                 is not set; every gRPC call is rejected as unauthenticated"
            );
            Ok(Arc::new(RejectingSubjectResolver))
        }
    }
}

struct VerificationKey {
    key: DecodingKey,
    algorithm: Algorithm,
    /// jsonwebtoken требует одно семейство алгоритмов на `Validation`,
    /// поэтому каждый ключ проверяется ровно своим алгоритмом.
    validation: Validation,
}

pub struct ServiceTokenSubjectResolver {
    keys: HashMap<String, VerificationKey>,
    /// `system:serviceaccount:<ns>:<sa>` → имя сервиса.
    service_accounts: HashMap<String, String>,
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

#[derive(Deserialize)]
struct Claims {
    sub: String,
}

impl ServiceTokenSubjectResolver {
    /// `Ok(None)`, если проверка не настроена (пусты issuer, audience или
    /// путь к JWKS). Нечитаемый JWKS и невалидный allowlist — ошибка.
    pub fn from_config(config: &IdentityConfig) -> Result<Option<Self>, IdentityConfigError> {
        // Невалидная user-конфигурация — ошибка startup, даже если service
        // identity выключена.
        let users = UserAssertionVerifier::from_config(config)?;
        let issuer = config.issuer.trim();
        let audience = config.audience.trim();
        let jwks_file = config.jwks_file.trim();
        if issuer.is_empty() || audience.is_empty() || jwks_file.is_empty() {
            return Ok(None);
        }
        let jwks = std::fs::read_to_string(jwks_file)
            .map_err(|error| config_error(format!("read JWKS {jwks_file}: {error}")))?;
        Self::new(issuer, audience, &jwks, &config.service_accounts)
            .map(|resolver| Some(resolver.with_user_assertions(users)))
    }

    pub fn new(
        issuer: &str,
        audience: &str,
        jwks: &str,
        service_accounts: &str,
    ) -> Result<Self, IdentityConfigError> {
        if issuer.is_empty() || audience.is_empty() {
            return Err(config_error("issuer and audience must not be empty"));
        }
        let mut keys = parse_jwks(jwks)?;
        let service_accounts = parse_service_accounts(service_accounts)?;

        for key in keys.values_mut() {
            let mut validation = Validation::new(key.algorithm);
            validation.set_required_spec_claims(&["exp", "iss", "aud", "sub"]);
            validation.set_issuer(&[issuer]);
            validation.set_audience(&[audience]);
            validation.leeway = CLOCK_LEEWAY_SECONDS;
            validation.validate_exp = true;
            validation.validate_nbf = true;
            validation.validate_aud = true;
            key.validation = validation;
        }

        Ok(Self {
            keys,
            service_accounts,
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
        let service = self.verify_service(metadata)?;
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
        eprintln!(
            "delivery: user assertion accepted: jti={:?} delegate={service:?}",
            user.jti
        );
        Ok(Caller {
            subject: Subject::User(user.sub),
            delegate: Some(service),
        })
    }

    fn verify_service(&self, metadata: &MetadataMap) -> Result<String, String> {
        let header = metadata
            .get(AUTHORIZATION)
            .ok_or("missing authorization metadata")?
            .to_str()
            .map_err(|_| "authorization metadata is not ASCII")?;
        let token = header
            .strip_prefix(BEARER)
            .ok_or("authorization scheme is not Bearer")?
            .trim();
        if token.is_empty() {
            return Err("empty bearer token".to_owned());
        }

        let jwt_header = decode_header(token).map_err(|error| format!("token header: {error}"))?;
        let kid = jwt_header.kid.as_deref().ok_or("token header has no kid")?;
        let key = self
            .keys
            .get(kid)
            .ok_or_else(|| format!("unknown kid {kid:?}"))?;
        if jwt_header.alg != key.algorithm {
            return Err(format!(
                "token alg {:?} does not match key {kid:?} ({:?})",
                jwt_header.alg, key.algorithm
            ));
        }

        let claims = decode::<Claims>(token, &key.key, &key.validation)
            .map_err(|error| format!("token rejected: {error}"))?
            .claims;
        self.service_accounts
            .get(&claims.sub)
            .cloned()
            .ok_or_else(|| format!("service account {:?} is not allowed", claims.sub))
    }
}

impl SubjectResolver for ServiceTokenSubjectResolver {
    fn resolve(&self, metadata: &MetadataMap) -> Result<Subject, Status> {
        self.authenticate(metadata)
            .map(|caller| caller.subject)
            .map_err(|reason| {
                eprintln!("delivery: unauthenticated gRPC call: {reason}");
                Status::unauthenticated(UNAUTHENTICATED)
            })
    }
}

/// Текущее время в секундах Unix; подменяется в тестах векторов.
pub type Clock = Arc<dyn Fn() -> u64 + Send + Sync>;

/// Проверка user assertion BFF (ADR 32): ES256 по JWKS BFF с обязательным
/// `kid`, `iss`, `aud`, срок по [`Clock`] с допуском
/// [`CLOCK_LEEWAY_SECONDS`], `exp - iat` не больше
/// [`MAX_USER_ASSERTION_TTL_SECONDS`], непустые `sub` и `jti`.
pub struct UserAssertionVerifier {
    keys: HashMap<String, DecodingKey>,
    /// Подпись, `iss` и `aud`; сроки проверяются по `clock`, потому что
    /// jsonwebtoken читает только системные часы.
    validation: Validation,
    delegates: HashSet<String>,
    clock: Clock,
}

#[derive(Deserialize)]
struct UserClaims {
    sub: String,
    jti: Option<String>,
    iat: Option<u64>,
    nbf: Option<u64>,
    exp: u64,
}

struct VerifiedUser {
    sub: String,
    jti: String,
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

    pub fn new(
        issuer: &str,
        audience: &str,
        jwks: &str,
        delegates: &str,
    ) -> Result<Self, IdentityConfigError> {
        if issuer.is_empty() || audience.is_empty() {
            return Err(config_error("user issuer and audience must not be empty"));
        }
        let keys = parse_user_jwks(jwks)?;
        let delegates = parse_delegates(delegates)?;

        let mut validation = Validation::new(Algorithm::ES256);
        validation.set_required_spec_claims(&["exp", "iss", "aud", "sub"]);
        validation.set_issuer(&[issuer]);
        validation.set_audience(&[audience]);
        validation.validate_exp = false;
        validation.validate_nbf = false;
        validation.validate_aud = true;

        Ok(Self {
            keys,
            validation,
            delegates,
            clock: Arc::new(jsonwebtoken::get_current_timestamp),
        })
    }

    pub fn with_clock(mut self, clock: Clock) -> Self {
        self.clock = clock;
        self
    }

    fn verify(&self, delegate: &str, token: &str) -> Result<VerifiedUser, String> {
        if !self.delegates.contains(delegate) {
            return Err(format!(
                "user assertion from {delegate:?} rejected: service is not a user delegate"
            ));
        }
        let rejected =
            |reason: String| format!("user assertion from {delegate:?} rejected: {reason}");

        let header = decode_header(token).map_err(|error| rejected(format!("header: {error}")))?;
        if header.alg != Algorithm::ES256 {
            return Err(rejected(format!("alg {:?} is not ES256", header.alg)));
        }
        let kid = header
            .kid
            .as_deref()
            .ok_or_else(|| rejected("header has no kid".to_owned()))?;
        let key = self
            .keys
            .get(kid)
            .ok_or_else(|| rejected(format!("unknown kid {kid:?}")))?;
        let claims = decode::<UserClaims>(token, key, &self.validation)
            .map_err(|error| rejected(error.to_string()))?
            .claims;

        let jti = claims
            .jti
            .as_deref()
            .map(str::trim)
            .filter(|jti| !jti.is_empty())
            .ok_or_else(|| rejected("missing jti".to_owned()))?
            .to_owned();
        check_user_claims(&claims, (self.clock)())
            .map_err(|reason| rejected(format!("jti={jti:?}: {reason}")))?;
        Ok(VerifiedUser {
            sub: claims.sub,
            jti,
        })
    }
}

/// Сроки и `sub` после проверки подписи, `iss` и `aud`.
fn check_user_claims(claims: &UserClaims, now: u64) -> Result<(), &'static str> {
    let iat = claims.iat.ok_or("missing iat")?;
    if claims.exp.saturating_add(CLOCK_LEEWAY_SECONDS) < now {
        return Err("expired");
    }
    if claims
        .nbf
        .is_some_and(|nbf| nbf > now.saturating_add(CLOCK_LEEWAY_SECONDS))
    {
        return Err("not yet valid");
    }
    match claims.exp.checked_sub(iat) {
        None => return Err("exp is before iat"),
        Some(ttl) if ttl > MAX_USER_ASSERTION_TTL_SECONDS => return Err("lifetime exceeds 60 s"),
        Some(_) => {}
    }
    if claims.sub.is_empty() || claims.sub.trim() != claims.sub {
        return Err("sub is empty or has surrounding whitespace");
    }
    Ok(())
}

/// JWKS BFF: только ключи EC P-256 для ES256, остальные игнорируются.
fn parse_user_jwks(jwks: &str) -> Result<HashMap<String, DecodingKey>, IdentityConfigError> {
    let keys: HashMap<String, DecodingKey> = parse_jwks(jwks)
        .map_err(|error| config_error(format!("BFF JWKS: {}", error.0)))?
        .into_iter()
        .filter_map(|(kid, key)| {
            if key.algorithm == Algorithm::ES256 {
                Some((kid, key.key))
            } else {
                eprintln!("delivery: warning: BFF JWKS key {kid:?} is not ES256 and is ignored");
                None
            }
        })
        .collect();
    if keys.is_empty() {
        return Err(config_error("BFF JWKS has no ES256 keys"));
    }
    Ok(keys)
}

fn parse_delegates(value: &str) -> Result<HashSet<String>, IdentityConfigError> {
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

fn parse_jwks(jwks: &str) -> Result<HashMap<String, VerificationKey>, IdentityConfigError> {
    let set: JwkSet =
        serde_json::from_str(jwks).map_err(|error| config_error(format!("parse JWKS: {error}")))?;
    let mut keys = HashMap::new();
    for jwk in &set.keys {
        let kid = jwk
            .common
            .key_id
            .clone()
            .filter(|kid| !kid.is_empty())
            .ok_or_else(|| config_error("JWKS key without kid"))?;
        let Some(algorithm) = verification_algorithm(jwk) else {
            eprintln!(
                "delivery: warning: JWKS key {kid:?} is not usable for RS256/ES256 and is ignored"
            );
            continue;
        };
        let key = DecodingKey::from_jwk(jwk)
            .map_err(|error| config_error(format!("JWKS key {kid:?}: {error}")))?;
        if keys
            .insert(
                kid.clone(),
                VerificationKey {
                    key,
                    algorithm,
                    validation: Validation::new(algorithm),
                },
            )
            .is_some()
        {
            return Err(config_error(format!("duplicate JWKS kid {kid:?}")));
        }
    }
    if keys.is_empty() {
        return Err(config_error("JWKS has no RS256/ES256 keys"));
    }
    Ok(keys)
}

/// RSA → RS256, EC P-256 → ES256; явный `alg` ключа должен совпадать.
fn verification_algorithm(jwk: &Jwk) -> Option<Algorithm> {
    let (algorithm, key_algorithm) = match &jwk.algorithm {
        AlgorithmParameters::RSA(_) => (Algorithm::RS256, KeyAlgorithm::RS256),
        AlgorithmParameters::EllipticCurve(params) if params.curve == EllipticCurve::P256 => {
            (Algorithm::ES256, KeyAlgorithm::ES256)
        }
        _ => return None,
    };
    match jwk.common.key_algorithm {
        None => Some(algorithm),
        Some(declared) if declared == key_algorithm => Some(algorithm),
        Some(_) => None,
    }
}

fn parse_service_accounts(value: &str) -> Result<HashMap<String, String>, IdentityConfigError> {
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

/// Общие тестовые векторы `tests/identity` (AC-461).
#[cfg(test)]
pub(crate) mod fixtures;

#[cfg(test)]
mod tests;
