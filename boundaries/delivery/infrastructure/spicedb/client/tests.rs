use super::*;

fn config(endpoint: &str, insecure: bool) -> SpiceDbConfig {
    SpiceDbConfig {
        endpoint: endpoint.to_owned(),
        preshared_key: "devkey".to_owned(),
        insecure,
        timeout: Duration::from_millis(200),
    }
}

fn response(permissionship: Permissionship) -> CheckPermissionResponse {
    CheckPermissionResponse {
        permissionship: permissionship as i32,
        ..CheckPermissionResponse::default()
    }
}

#[test]
fn only_has_permission_allows() {
    assert!(allowed(&response(Permissionship::HasPermission)));
    assert!(!allowed(&response(Permissionship::NoPermission)));
    assert!(!allowed(&response(Permissionship::ConditionalPermission)));
    assert!(!allowed(&response(Permissionship::Unspecified)));
    assert!(!allowed(&CheckPermissionResponse {
        permissionship: 42,
        ..CheckPermissionResponse::default()
    }));
}

#[test]
fn request_uses_minimize_latency_and_subject_object() {
    let request = request(&PermissionCheck {
        resource_type: "platform",
        resource_id: "nomnom".to_owned(),
        permission: "coordinate_delivery",
        subject_type: "service",
        subject_id: "orders-worker".to_owned(),
        consistency: CheckConsistency::MinimizeLatency,
    });

    assert_eq!(
        request.consistency,
        Some(Consistency {
            requirement: Some(Requirement::MinimizeLatency(true))
        })
    );
    assert_eq!(request.permission, "coordinate_delivery");
    let resource = request.resource.expect("resource");
    assert_eq!(
        (resource.object_type.as_str(), resource.object_id.as_str()),
        ("platform", "nomnom")
    );
    let subject = request
        .subject
        .and_then(|subject| subject.object)
        .expect("subject");
    assert_eq!(
        (subject.object_type.as_str(), subject.object_id.as_str()),
        ("service", "orders-worker")
    );
}

#[test]
fn request_passes_stored_revision_as_at_least_as_fresh() {
    let request = request(&PermissionCheck {
        resource_type: "delivery",
        resource_id: "delivery-1".to_owned(),
        permission: "act_as_courier",
        subject_type: "user",
        subject_id: "courier-1".to_owned(),
        consistency: CheckConsistency::AtLeastAsFresh(AuthorizationRevision::new("zed-1")),
    });

    assert_eq!(
        request.consistency,
        Some(Consistency {
            requirement: Some(Requirement::AtLeastAsFresh(ZedToken {
                token: "zed-1".to_owned()
            }))
        })
    );
}

#[test]
fn write_revision_is_written_at_token() {
    let response = |token: Option<&str>| WriteRelationshipsResponse {
        written_at: token.map(|token| ZedToken {
            token: token.to_owned(),
        }),
    };

    assert_eq!(
        written_at(&response(Some("zed-1"))),
        Ok(AuthorizationRevision::new("zed-1"))
    );
    assert!(written_at(&response(None)).is_err());
    assert!(written_at(&response(Some(""))).is_err());
}

#[test]
fn endpoint_scheme_follows_insecure() {
    assert_eq!(
        endpoint(&config("localhost:50051", true))
            .expect("plaintext")
            .uri()
            .to_string(),
        "http://localhost:50051/"
    );
    assert_eq!(
        endpoint(&config("spicedb.example:443", false))
            .expect("tls")
            .uri()
            .to_string(),
        "https://spicedb.example:443/"
    );
}

#[test]
fn rejects_scheme_conflicting_with_insecure() {
    assert!(endpoint(&config("http://spicedb:50051", false)).is_err());
    assert!(endpoint(&config("https://spicedb:50051", true)).is_err());
}

#[test]
fn rejects_missing_key() {
    let mut config = config("localhost:50051", true);
    config.preshared_key.clear();

    assert!(SpiceDbClient::connect(&config).is_err());
}

#[test]
fn unreachable_endpoint_is_an_error_not_a_decision() {
    let client = SpiceDbClient::connect(&config("127.0.0.1:1", true)).expect("client");
    let outcome = client.check(&PermissionCheck {
        resource_type: "platform",
        resource_id: "nomnom".to_owned(),
        permission: "coordinate_delivery",
        subject_type: "service",
        subject_id: "orders-worker".to_owned(),
        consistency: CheckConsistency::MinimizeLatency,
    });

    assert!(outcome.is_err(), "{outcome:?}");
}

#[test]
fn silent_endpoint_times_out_within_deadline() {
    // Принимает TCP, но не отвечает gRPC: ответа нет в пределах timeout.
    let silent = std::net::TcpListener::bind("127.0.0.1:0").expect("bind");
    let address = silent.local_addr().expect("addr");
    let client = SpiceDbClient::connect(&config(&address.to_string(), true)).expect("client");
    let started = std::time::Instant::now();

    let outcome = client.check(&PermissionCheck {
        resource_type: "platform",
        resource_id: "nomnom".to_owned(),
        permission: "coordinate_delivery",
        subject_type: "service",
        subject_id: "orders-worker".to_owned(),
        consistency: CheckConsistency::MinimizeLatency,
    });

    assert!(outcome.is_err(), "{outcome:?}");
    assert!(started.elapsed() < Duration::from_millis(200) + REPLY_GRACE);
}
