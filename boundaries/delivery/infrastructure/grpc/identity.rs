//! Service identity по projected ServiceAccount token (ADR 27).
//!
//! [`ServiceTokenSubjectResolver`] проверяет `authorization: Bearer <jwt>` по
//! JWKS кластера и переводит ServiceAccount из allowlist в
//! [`Subject::Service`]. Любой отказ — `UNAUTHENTICATED` с общим сообщением;
//! причина пишется только в log.

use std::collections::HashMap;
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

const AUTHORIZATION: &str = "authorization";
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
}

#[derive(Deserialize)]
struct Claims {
    sub: String,
}

impl ServiceTokenSubjectResolver {
    /// `Ok(None)`, если проверка не настроена (пусты issuer, audience или
    /// путь к JWKS). Нечитаемый JWKS и невалидный allowlist — ошибка.
    pub fn from_config(config: &IdentityConfig) -> Result<Option<Self>, IdentityConfigError> {
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
        })
    }

    fn verify(&self, metadata: &MetadataMap) -> Result<Subject, String> {
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
            .map(|service| Subject::Service(service.clone()))
            .ok_or_else(|| format!("service account {:?} is not allowed", claims.sub))
    }
}

impl SubjectResolver for ServiceTokenSubjectResolver {
    fn resolve(&self, metadata: &MetadataMap) -> Result<Subject, Status> {
        self.verify(metadata).map_err(|reason| {
            eprintln!("delivery: unauthenticated gRPC call: {reason}");
            Status::unauthenticated(UNAUTHENTICATED)
        })
    }
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
}
