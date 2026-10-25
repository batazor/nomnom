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
fn requires_service_identity_configuration() {
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
        assert!(subject_resolver(&config).is_err());
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
    let unconfigured = IdentityConfig {
        user_issuer: String::new(),
        user_jwks_file: String::new(),
        user_delegates: String::new(),
        ..user_identity()
    };
    let partial = [
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

    for config in partial {
        assert!(UserAssertionVerifier::from_config(&config).is_err());
    }

    {
        let config = unconfigured;
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
