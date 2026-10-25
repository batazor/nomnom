//! Проверка user assertion BFF (ADR 32).

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use jsonwebtoken::{Algorithm, DecodingKey, Validation, decode, decode_header};
use serde::Deserialize;

use super::CLOCK_LEEWAY_SECONDS;
use super::config::{IdentityConfigError, config_error, parse_delegates};
use super::jwks::parse_user_jwks;

/// Максимальный `exp - iat` user assertion (ADR 32).
pub const MAX_USER_ASSERTION_TTL_SECONDS: u64 = 60;

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

pub(super) struct VerifiedUser {
    pub(super) sub: String,
    pub(super) jti: String,
}

impl UserAssertionVerifier {
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

    pub(super) fn verify(&self, delegate: &str, token: &str) -> Result<VerifiedUser, String> {
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
