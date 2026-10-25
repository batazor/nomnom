//! Разбор JWKS: ключи кластера для service token (RS256/ES256) и ключи BFF
//! для user assertion (только ES256).

use std::collections::HashMap;

use jsonwebtoken::jwk::{AlgorithmParameters, EllipticCurve, Jwk, JwkSet, KeyAlgorithm};
use jsonwebtoken::{Algorithm, DecodingKey, Validation};

use super::config::{IdentityConfigError, config_error};

pub(super) struct VerificationKey {
    pub(super) key: DecodingKey,
    pub(super) algorithm: Algorithm,
    /// jsonwebtoken требует одно семейство алгоритмов на `Validation`,
    /// поэтому каждый ключ проверяется ровно своим алгоритмом.
    pub(super) validation: Validation,
}

/// JWKS BFF: только ключи EC P-256 для ES256, остальные игнорируются.
pub(super) fn parse_user_jwks(
    jwks: &str,
) -> Result<HashMap<String, DecodingKey>, IdentityConfigError> {
    let keys: HashMap<String, DecodingKey> = parse_jwks(jwks)
        .map_err(|error| config_error(format!("BFF JWKS: {}", error.0)))?
        .into_iter()
        .filter_map(|(kid, key)| {
            if key.algorithm == Algorithm::ES256 {
                Some((kid, key.key))
            } else {
                tracing::warn!(operation = "identity.jwks", kid = ?kid, "BFF JWKS key is not ES256 and is ignored");
                None
            }
        })
        .collect();
    if keys.is_empty() {
        return Err(config_error("BFF JWKS has no ES256 keys"));
    }
    Ok(keys)
}

pub(super) fn parse_jwks(
    jwks: &str,
) -> Result<HashMap<String, VerificationKey>, IdentityConfigError> {
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
            tracing::warn!(
                operation = "identity.jwks",
                kid = ?kid,
                "JWKS key is not usable for RS256/ES256 and is ignored"
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
