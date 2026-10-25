//! Проверка projected ServiceAccount token (ADR 27): `authorization: Bearer
//! <jwt>` по JWKS кластера, ServiceAccount из allowlist → имя сервиса.

use std::collections::HashMap;

use jsonwebtoken::{Validation, decode, decode_header};
use serde::Deserialize;
use tonic::metadata::MetadataMap;

use super::CLOCK_LEEWAY_SECONDS;
use super::config::{IdentityConfigError, config_error, parse_service_accounts};
use super::jwks::{VerificationKey, parse_jwks};

pub(super) const AUTHORIZATION: &str = "authorization";
const BEARER: &str = "Bearer ";

pub(super) struct ServiceTokenVerifier {
    keys: HashMap<String, VerificationKey>,
    /// `system:serviceaccount:<ns>:<sa>` → имя сервиса.
    service_accounts: HashMap<String, String>,
}

#[derive(Deserialize)]
struct Claims {
    sub: String,
}

impl ServiceTokenVerifier {
    pub(super) fn new(
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

    /// Имя сервиса вызывающего; ошибка — причина отказа для log, без token.
    pub(super) fn verify(&self, metadata: &MetadataMap) -> Result<String, String> {
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
