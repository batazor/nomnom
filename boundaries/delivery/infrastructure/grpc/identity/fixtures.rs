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
    let raw =
        std::fs::read_to_string(dir().join("user-vectors.json")).expect("read user-vectors.json");
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
