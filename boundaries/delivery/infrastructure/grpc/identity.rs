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
pub(crate) mod fixtures {
    use std::collections::HashMap;
    use std::path::PathBuf;

    use serde::Deserialize;

    #[derive(Deserialize)]
    pub(crate) struct Vectors {
        pub issuer: String,
        pub audience: String,
        pub service_accounts: String,
        pub expected_service: String,
        pub accepted: Vec<String>,
        pub rejected: Vec<String>,
        pub tokens: HashMap<String, String>,
    }

    impl Vectors {
        pub(crate) fn token(&self, name: &str) -> &str {
            self.tokens
                .get(name)
                .unwrap_or_else(|| panic!("vector {name} has no token"))
        }
    }

    pub(crate) fn dir() -> PathBuf {
        PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../tests/identity/fixtures")
    }

    pub(crate) fn jwks_path() -> PathBuf {
        dir().join("jwks.json")
    }

    pub(crate) fn jwks() -> String {
        std::fs::read_to_string(jwks_path()).expect("read jwks.json")
    }

    pub(crate) fn vectors() -> Vectors {
        let raw = std::fs::read_to_string(dir().join("vectors.json")).expect("read vectors.json");
        serde_json::from_str(&raw).expect("parse vectors.json")
    }

    pub(crate) fn resolver() -> super::ServiceTokenSubjectResolver {
        let vectors = vectors();
        super::ServiceTokenSubjectResolver::new(
            &vectors.issuer,
            &vectors.audience,
            &jwks(),
            &vectors.service_accounts,
        )
        .expect("resolver from fixtures")
    }

    /// Векторы user assertions (ADR 32); assertions действительны только
    /// при часах, зафиксированных на `now`.
    #[derive(Deserialize)]
    pub(crate) struct UserVectors {
        pub issuer: String,
        pub audience: String,
        pub now: u64,
        pub delegates: String,
        pub service_issuer: String,
        pub service_accounts: String,
        pub delegate_token: String,
        pub non_delegate_token: String,
        pub expected_subject: String,
        pub accepted: Vec<String>,
        pub rejected: Vec<String>,
        pub tokens: HashMap<String, String>,
    }

    impl UserVectors {
        pub(crate) fn token(&self, name: &str) -> &str {
            self.tokens
                .get(name)
                .unwrap_or_else(|| panic!("user vector {name} has no token"))
        }

        pub(crate) fn clock(&self) -> super::Clock {
            let now = self.now;
            std::sync::Arc::new(move || now)
        }
    }

    pub(crate) fn bff_jwks_path() -> PathBuf {
        dir().join("bff-jwks.json")
    }

    pub(crate) fn bff_jwks() -> String {
        std::fs::read_to_string(bff_jwks_path()).expect("read bff-jwks.json")
    }

    pub(crate) fn user_vectors() -> UserVectors {
        let raw = std::fs::read_to_string(dir().join("user-vectors.json"))
            .expect("read user-vectors.json");
        serde_json::from_str(&raw).expect("parse user-vectors.json")
    }

    /// Service identity без user assertions, как контекст без user-конфигурации.
    pub(crate) fn service_resolver_for_users() -> super::ServiceTokenSubjectResolver {
        let vectors = user_vectors();
        super::ServiceTokenSubjectResolver::new(
            &vectors.service_issuer,
            &vectors.audience,
            &jwks(),
            &vectors.service_accounts,
        )
        .expect("service resolver from user fixtures")
    }

    pub(crate) fn user_resolver() -> super::ServiceTokenSubjectResolver {
        let vectors = user_vectors();
        let users = super::UserAssertionVerifier::new(
            &vectors.issuer,
            &vectors.audience,
            &bff_jwks(),
            &vectors.delegates,
        )
        .expect("user verifier from fixtures")
        .with_clock(vectors.clock());
        service_resolver_for_users().with_user_assertions(Some(users))
    }
}

#[cfg(test)]
mod tests {
    use tonic::Code;
    use tonic::metadata::MetadataValue;

    use super::*;

    fn bearer(token: &str) -> MetadataMap {
        authorization(&format!("Bearer {token}"))
    }

    fn authorization(value: &str) -> MetadataMap {
        let mut metadata = MetadataMap::new();
        metadata.insert(
            AUTHORIZATION,
            MetadataValue::try_from(value).expect("ascii metadata"),
        );
        metadata
    }

    fn identity(issuer: &str, audience: &str, jwks_file: &str, accounts: &str) -> IdentityConfig {
        IdentityConfig {
            issuer: issuer.to_owned(),
            audience: audience.to_owned(),
            jwks_file: jwks_file.to_owned(),
            service_accounts: accounts.to_owned(),
            ..IdentityConfig::default()
        }
    }

    fn fixture_identity() -> IdentityConfig {
        let vectors = fixtures::vectors();
        identity(
            &vectors.issuer,
            &vectors.audience,
            fixtures::jwks_path().to_str().expect("utf-8 path"),
            &vectors.service_accounts,
        )
    }

    #[test]
    fn accepts_only_the_accepted_vectors() {
        let vectors = fixtures::vectors();
        let resolver = fixtures::resolver();
        assert!(!vectors.accepted.is_empty() && !vectors.rejected.is_empty());

        for name in &vectors.accepted {
            let subject = resolver
                .resolve(&bearer(vectors.token(name)))
                .unwrap_or_else(|status| panic!("{name} must be accepted: {status:?}"));
            assert_eq!(
                subject,
                Subject::Service(vectors.expected_service.clone()),
                "{name}"
            );
            assert_eq!(subject, Subject::Service("orders-worker".to_owned()));
        }
        for name in &vectors.rejected {
            let status = resolver
                .resolve(&bearer(vectors.token(name)))
                .expect_err(&format!("{name} must be rejected"));
            assert_eq!(status.code(), Code::Unauthenticated, "{name}");
            assert_eq!(status.message(), UNAUTHENTICATED, "{name}: generic message");
        }
    }

    #[test]
    fn rejects_missing_or_non_bearer_authorization() {
        let vectors = fixtures::vectors();
        let resolver = fixtures::resolver();
        let token = vectors.token("valid");

        for metadata in [
            MetadataMap::new(),
            authorization(token),
            authorization(&format!("Basic {token}")),
            authorization(&format!("bearer{token}")),
            authorization("Bearer "),
        ] {
            let status = resolver.resolve(&metadata).expect_err("rejected");
            assert_eq!(status.code(), Code::Unauthenticated);
        }
    }

    #[test]
    fn token_for_another_audience_is_rejected() {
        let vectors = fixtures::vectors();
        let resolver = ServiceTokenSubjectResolver::new(
            &vectors.issuer,
            "nomnom.delivery",
            &fixtures::jwks(),
            &vectors.service_accounts,
        )
        .expect("resolver");

        let status = resolver
            .resolve(&bearer(vectors.token("valid")))
            .expect_err("payments token presented to delivery");
        assert_eq!(status.code(), Code::Unauthenticated);
    }

    #[test]
    fn falls_back_to_rejecting_resolver_without_configuration() {
        let configured = fixture_identity();
        let unconfigured = [
            IdentityConfig::default(),
            IdentityConfig {
                issuer: String::new(),
                ..fixture_identity()
            },
            IdentityConfig {
                audience: String::new(),
                ..fixture_identity()
            },
            IdentityConfig {
                jwks_file: String::new(),
                ..fixture_identity()
            },
        ];
        let token = fixtures::vectors().token("valid").to_owned();

        for config in unconfigured {
            assert!(
                ServiceTokenSubjectResolver::from_config(&config)
                    .expect("not an error")
                    .is_none()
            );
            let resolver = subject_resolver(&config).expect("fallback");
            let status = resolver.resolve(&bearer(&token)).expect_err("rejected");
            assert_eq!(status.code(), Code::Unauthenticated);
        }

        let resolver = subject_resolver(&configured).expect("configured");
        assert_eq!(
            resolver.resolve(&bearer(&token)).expect("verified"),
            Subject::Service("orders-worker".to_owned())
        );
    }

    #[test]
    fn unreadable_jwks_file_is_a_startup_error() {
        let config = IdentityConfig {
            jwks_file: fixtures::dir()
                .join("missing-jwks.json")
                .to_string_lossy()
                .into_owned(),
            ..fixture_identity()
        };

        assert!(subject_resolver(&config).is_err());
    }

    #[test]
    fn invalid_jwks_is_a_startup_error() {
        let vectors = fixtures::vectors();
        for jwks in [
            "not json",
            "{}",
            r#"{"keys": []}"#,
            r#"{"keys": [{"kty": "oct", "kid": "hmac", "k": "c2VjcmV0"}]}"#,
            r#"{"keys": [{"kty": "RSA", "n": "AQAB", "e": "AQAB"}]}"#,
        ] {
            assert!(
                ServiceTokenSubjectResolver::new(
                    &vectors.issuer,
                    &vectors.audience,
                    jwks,
                    &vectors.service_accounts
                )
                .is_err(),
                "{jwks}"
            );
        }
    }

    #[test]
    fn empty_or_malformed_allowlist_is_a_startup_error() {
        let vectors = fixtures::vectors();
        for accounts in [
            "",
            "   ",
            "orders-worker",
            "nomnom/orders-worker",
            "nomnom=orders-worker",
            "/orders-worker=orders-worker",
            "nomnom/=orders-worker",
            "nomnom/orders-worker=",
            "nomnom/orders-worker=orders-worker,",
            "nomnom/a/b=orders-worker",
            "nomnom/orders-worker=orders-worker,nomnom/orders-worker=other",
        ] {
            let config = IdentityConfig {
                service_accounts: accounts.to_owned(),
                ..fixture_identity()
            };
            assert!(subject_resolver(&config).is_err(), "{accounts:?}");
        }
        assert!(
            ServiceTokenSubjectResolver::new(
                &vectors.issuer,
                &vectors.audience,
                &fixtures::jwks(),
                " nomnom/orders-worker = orders-worker , jobs/cleanup=cleanup-job ",
            )
            .is_ok()
        );
    }

    fn with_assertion(service_token: &str, assertion: &str) -> MetadataMap {
        let mut metadata = bearer(service_token);
        metadata.insert(
            USER_ASSERTION,
            MetadataValue::try_from(assertion).expect("ascii metadata"),
        );
        metadata
    }

    fn path(path: std::path::PathBuf) -> String {
        path.to_str().expect("utf-8 path").to_owned()
    }

    /// Service и user identity из `user-vectors.json`.
    fn user_identity() -> IdentityConfig {
        let vectors = fixtures::user_vectors();
        IdentityConfig {
            user_issuer: vectors.issuer.clone(),
            user_jwks_file: path(fixtures::bff_jwks_path()),
            user_delegates: vectors.delegates.clone(),
            ..identity(
                &vectors.service_issuer,
                &vectors.audience,
                &path(fixtures::jwks_path()),
                &vectors.service_accounts,
            )
        }
    }

    #[test]
    fn accepts_only_the_accepted_user_vectors() {
        let vectors = fixtures::user_vectors();
        let resolver = fixtures::user_resolver();
        assert!(!vectors.accepted.is_empty() && !vectors.rejected.is_empty());

        for name in &vectors.accepted {
            let metadata = with_assertion(&vectors.delegate_token, vectors.token(name));
            let caller = resolver
                .authenticate(&metadata)
                .unwrap_or_else(|reason| panic!("{name} must be accepted: {reason}"));
            assert_eq!(
                caller,
                Caller {
                    subject: Subject::User(vectors.expected_subject.clone()),
                    delegate: Some("bff".to_owned()),
                },
                "{name}"
            );
            assert_eq!(
                resolver.resolve(&metadata).expect("accepted"),
                Subject::User("customer-1".to_owned())
            );
        }
        for name in &vectors.rejected {
            let status = resolver
                .resolve(&with_assertion(
                    &vectors.delegate_token,
                    vectors.token(name),
                ))
                .expect_err(&format!("{name} must be rejected"));
            assert_eq!(status.code(), Code::Unauthenticated, "{name}");
            assert_eq!(status.message(), UNAUTHENTICATED, "{name}: generic message");
        }
    }

    #[test]
    fn delegate_without_assertion_stays_a_service_subject() {
        let vectors = fixtures::user_vectors();
        let caller = fixtures::user_resolver()
            .authenticate(&bearer(&vectors.delegate_token))
            .expect("service token");

        assert_eq!(
            caller,
            Caller {
                subject: Subject::Service("bff".to_owned()),
                delegate: None,
            }
        );
    }

    #[test]
    fn assertion_from_a_non_delegate_is_rejected() {
        // AC-526: валидный assertion с service token orders-worker.
        let vectors = fixtures::user_vectors();
        let resolver = fixtures::user_resolver();

        assert_eq!(
            resolver
                .resolve(&bearer(&vectors.non_delegate_token))
                .expect("service token alone"),
            Subject::Service("orders-worker".to_owned())
        );
        let status = resolver
            .resolve(&with_assertion(
                &vectors.non_delegate_token,
                vectors.token("valid"),
            ))
            .expect_err("non-delegate");
        assert_eq!(status.code(), Code::Unauthenticated);
    }

    #[test]
    fn assertion_requires_a_service_token() {
        let vectors = fixtures::user_vectors();
        let resolver = fixtures::user_resolver();
        let mut metadata = MetadataMap::new();
        metadata.insert(
            USER_ASSERTION,
            MetadataValue::try_from(vectors.token("valid")).expect("ascii metadata"),
        );

        let status = resolver.resolve(&metadata).expect_err("no service token");
        assert_eq!(status.code(), Code::Unauthenticated);
    }

    #[test]
    fn repeated_or_empty_assertion_is_rejected() {
        let vectors = fixtures::user_vectors();
        let resolver = fixtures::user_resolver();
        let valid = vectors.token("valid");

        let mut repeated = with_assertion(&vectors.delegate_token, valid);
        repeated.append(
            USER_ASSERTION,
            MetadataValue::try_from(valid).expect("ascii metadata"),
        );
        for metadata in [
            repeated,
            with_assertion(&vectors.delegate_token, ""),
            with_assertion(&vectors.delegate_token, "   "),
        ] {
            let status = resolver.resolve(&metadata).expect_err("rejected");
            assert_eq!(status.code(), Code::Unauthenticated);
        }
    }

    #[test]
    fn assertion_outside_the_clock_window_is_rejected() {
        let vectors = fixtures::user_vectors();
        let valid = vectors.token("valid");
        // valid: iat = nbf = now - 10, exp = now + 20.
        for (now, accepted) in [
            (vectors.now - 10 - CLOCK_LEEWAY_SECONDS, true),
            (vectors.now - 11 - CLOCK_LEEWAY_SECONDS, false),
            (vectors.now + 20 + CLOCK_LEEWAY_SECONDS, true),
            (vectors.now + 21 + CLOCK_LEEWAY_SECONDS, false),
        ] {
            let users = UserAssertionVerifier::new(
                &vectors.issuer,
                &vectors.audience,
                &fixtures::bff_jwks(),
                &vectors.delegates,
            )
            .expect("verifier")
            .with_clock(Arc::new(move || now));
            let resolver = fixtures::service_resolver_for_users().with_user_assertions(Some(users));

            let result = resolver.authenticate(&with_assertion(&vectors.delegate_token, valid));
            assert_eq!(result.is_ok(), accepted, "now = {now}: {result:?}");
        }
    }

    #[test]
    fn resolver_without_user_settings_rejects_assertions() {
        // AC-529: без user-конфигурации assertion отклоняется, а тот же
        // вызов без assertion остаётся Subject{service, bff}.
        let vectors = fixtures::user_vectors();
        let full = user_identity();
        let unconfigured = [
            IdentityConfig {
                user_issuer: String::new(),
                user_jwks_file: String::new(),
                user_delegates: String::new(),
                ..user_identity()
            },
            IdentityConfig {
                user_jwks_file: String::new(),
                ..user_identity()
            },
            IdentityConfig {
                user_issuer: String::new(),
                ..user_identity()
            },
            IdentityConfig {
                user_delegates: "  ".to_owned(),
                ..user_identity()
            },
        ];
        assert!(
            UserAssertionVerifier::from_config(&full)
                .expect("valid")
                .is_some()
        );

        for config in unconfigured {
            assert!(
                UserAssertionVerifier::from_config(&config)
                    .expect("not an error")
                    .is_none()
            );
            let resolver = subject_resolver(&config).expect("service identity");
            let status = resolver
                .resolve(&with_assertion(
                    &vectors.delegate_token,
                    vectors.token("valid"),
                ))
                .expect_err("assertion without user configuration");
            assert_eq!(status.code(), Code::Unauthenticated);
            assert_eq!(
                resolver
                    .resolve(&bearer(&vectors.delegate_token))
                    .expect("service token"),
                Subject::Service("bff".to_owned())
            );
        }
    }

    #[test]
    fn configured_user_verifier_accepts_vectors_with_fixed_clock() {
        let vectors = fixtures::user_vectors();
        let config = user_identity();
        let users = UserAssertionVerifier::from_config(&config)
            .expect("valid")
            .expect("configured")
            .with_clock(vectors.clock());
        let resolver = ServiceTokenSubjectResolver::from_config(&config)
            .expect("valid")
            .expect("configured")
            .with_user_assertions(Some(users));

        assert_eq!(
            resolver
                .resolve(&with_assertion(
                    &vectors.delegate_token,
                    vectors.token("valid"),
                ))
                .expect("accepted"),
            Subject::User(vectors.expected_subject.clone())
        );
        // Системные часы: статический assertion давно истёк.
        let status = subject_resolver(&config)
            .expect("configured")
            .resolve(&with_assertion(
                &vectors.delegate_token,
                vectors.token("valid"),
            ))
            .expect_err("expired by the real clock");
        assert_eq!(status.code(), Code::Unauthenticated);
    }

    #[test]
    fn invalid_bff_jwks_is_a_startup_error() {
        let vectors = fixtures::user_vectors();
        for jwks in [
            "not json",
            "{}",
            r#"{"keys": []}"#,
            r#"{"keys": [{"kty": "oct", "kid": "hmac", "k": "c2VjcmV0"}]}"#,
            // JWKS кластера: только RSA, для ES256 непригоден.
            &fixtures::jwks(),
        ] {
            assert!(
                UserAssertionVerifier::new(
                    &vectors.issuer,
                    &vectors.audience,
                    jwks,
                    &vectors.delegates
                )
                .is_err(),
                "{jwks}"
            );
        }

        for user_jwks_file in [
            path(fixtures::dir().join("missing-bff-jwks.json")),
            path(fixtures::dir().join("user-vectors.json")),
            path(fixtures::jwks_path()),
        ] {
            let config = IdentityConfig {
                user_jwks_file: user_jwks_file.clone(),
                ..user_identity()
            };
            assert!(subject_resolver(&config).is_err(), "{user_jwks_file}");
            // Ошибка startup и без service identity.
            let config = IdentityConfig {
                issuer: String::new(),
                audience: String::new(),
                ..config
            };
            assert!(subject_resolver(&config).is_err(), "{user_jwks_file}");
        }
    }

    #[test]
    fn malformed_user_delegates_are_a_startup_error() {
        let vectors = fixtures::user_vectors();
        for delegates in [
            "bff,",
            ",bff",
            "bff,,admin",
            "nomnom/bff",
            "bff=bff",
            "b ff",
        ] {
            let config = IdentityConfig {
                user_delegates: delegates.to_owned(),
                ..user_identity()
            };
            assert!(subject_resolver(&config).is_err(), "{delegates:?}");
        }
        assert!(
            UserAssertionVerifier::new(
                &vectors.issuer,
                &vectors.audience,
                &fixtures::bff_jwks(),
                " bff , admin-bff ",
            )
            .is_ok()
        );
    }
}
