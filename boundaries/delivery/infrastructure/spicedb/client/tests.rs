use super::super::mapping::CheckConsistency;
use super::super::v1::check_permission_response::Permissionship;
use super::super::v1::consistency::Requirement;
use super::super::v1::{CheckPermissionResponse, Consistency, ZedToken};
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

mod telemetry {
    use std::convert::Infallible;
    use std::sync::{Arc, Mutex};
    use std::task::{Context as TaskContext, Poll};

    use opentelemetry::metrics::MeterProvider as _;
    use opentelemetry::trace::{
        SpanKind, Status, TraceContextExt as _, Tracer as _, TracerProvider as _,
    };
    use opentelemetry_sdk::metrics::data::{AggregatedMetrics, MetricData};
    use opentelemetry_sdk::metrics::{InMemoryMetricExporter, PeriodicReader, SdkMeterProvider};
    use opentelemetry_sdk::trace::{InMemorySpanExporter, SdkTracerProvider};
    use tonic::codegen::http;

    use super::super::super::mapping::CheckConsistency;
    use super::*;

    /// SpiceDB stand-in: records `traceparent` and denies with
    /// `PERMISSION_DENIED`.
    #[derive(Clone, Default)]
    struct DenyingSpiceDb {
        traceparents: Arc<Mutex<Vec<Option<String>>>>,
    }

    impl tonic::server::NamedService for DenyingSpiceDb {
        const NAME: &'static str = "authzed.api.v1.PermissionsService";
    }

    impl tonic::codegen::Service<http::Request<tonic::body::Body>> for DenyingSpiceDb {
        type Response = http::Response<tonic::body::Body>;
        type Error = Infallible;
        type Future = std::future::Ready<Result<Self::Response, Infallible>>;

        fn poll_ready(&mut self, _cx: &mut TaskContext<'_>) -> Poll<Result<(), Infallible>> {
            Poll::Ready(Ok(()))
        }

        fn call(&mut self, request: http::Request<tonic::body::Body>) -> Self::Future {
            self.traceparents.lock().expect("traceparents").push(
                request
                    .headers()
                    .get("traceparent")
                    .and_then(|value| value.to_str().ok())
                    .map(str::to_owned),
            );
            std::future::ready(Ok(tonic::Status::permission_denied("denied").into_http()))
        }
    }

    /// Serves `service` on a loopback port in its own runtime thread.
    fn serve(service: DenyingSpiceDb) -> std::net::SocketAddr {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").expect("bind");
        listener.set_nonblocking(true).expect("nonblocking");
        let address = listener.local_addr().expect("addr");
        std::thread::spawn(move || {
            let runtime = tokio::runtime::Builder::new_current_thread()
                .enable_all()
                .build()
                .expect("runtime");
            runtime.block_on(async move {
                let listener = tokio::net::TcpListener::from_std(listener).expect("listener");
                tonic::transport::Server::builder()
                    .add_service(service)
                    .serve_with_incoming(tonic::transport::server::TcpIncoming::from(listener))
                    .await
                    .expect("serve");
            });
        });
        address
    }

    #[test]
    fn check_carries_trace_context_and_records_client_telemetry() {
        let spans = InMemorySpanExporter::default();
        let tracer_provider = SdkTracerProvider::builder()
            .with_simple_exporter(spans.clone())
            .build();
        let metrics = InMemoryMetricExporter::default();
        let meter_provider = SdkMeterProvider::builder()
            .with_reader(PeriodicReader::builder(metrics.clone()).build())
            .build();
        let tracer = tracer_provider.tracer("test");
        let spicedb = DenyingSpiceDb::default();
        let address = serve(spicedb.clone());
        let mut config = config(&address.to_string(), true);
        config.timeout = Duration::from_secs(2);
        let client = SpiceDbClient::connect_with_telemetry(
            &config,
            ClientTelemetry::new(
                BoxedTracer::new(Box::new(tracer_provider.tracer(rpc::INSTRUMENTATION))),
                RpcMetrics::new(&meter_provider.meter(rpc::INSTRUMENTATION)),
            ),
        )
        .expect("client");

        let outcome = tracer.in_span("usecase create_delivery", |_| {
            client.check(&PermissionCheck {
                resource_type: "platform",
                resource_id: "nomnom".to_owned(),
                permission: "coordinate_delivery",
                subject_type: "service",
                subject_id: "orders-worker".to_owned(),
                consistency: CheckConsistency::MinimizeLatency,
            })
        });
        assert!(outcome.is_err(), "denied by status is not a decision");

        let finished = spans.get_finished_spans().expect("spans");
        let parent = finished
            .iter()
            .find(|span| span.name == "usecase create_delivery")
            .expect("use case span");
        let call = finished
            .iter()
            .find(|span| span.name == CHECK_PERMISSION)
            .expect("client span");
        assert_eq!(call.span_kind, SpanKind::Client);
        assert_eq!(call.parent_span_id, parent.span_context.span_id());
        assert_eq!(call.status, Status::error("PERMISSION_DENIED"));
        let traceparents = spicedb.traceparents.lock().expect("traceparents").clone();
        assert_eq!(
            traceparents,
            vec![Some(format!(
                "00-{}-{}-01",
                call.span_context.trace_id(),
                call.span_context.span_id()
            ))]
        );
        assert!(
            !call
                .attributes
                .iter()
                .any(|kv| kv.value.as_str().contains("devkey")),
            "the preshared key never reaches telemetry"
        );

        meter_provider.force_flush().expect("flush");
        let exported = metrics
            .get_finished_metrics()
            .expect("metrics")
            .pop()
            .expect("export");
        let metric = exported
            .scope_metrics()
            .flat_map(|scope| scope.metrics())
            .find(|metric| metric.name() == "rpc.client.call.duration")
            .expect("rpc.client.call.duration");
        let AggregatedMetrics::F64(MetricData::Histogram(histogram)) = metric.data() else {
            panic!("histogram");
        };
        let point = histogram.data_points().next().expect("point");
        let attribute = |key: &str| {
            point
                .attributes()
                .find(|kv| kv.key.as_str() == key)
                .map(|kv| kv.value.as_str().into_owned())
        };
        assert_eq!(attribute("rpc.method").as_deref(), Some(CHECK_PERMISSION));
        assert_eq!(
            attribute("rpc.response.status_code").as_deref(),
            Some("PERMISSION_DENIED")
        );
        assert_eq!(attribute("server.address").as_deref(), Some("127.0.0.1"));
        assert_eq!(attribute("server.port"), Some(address.port().to_string()));
    }

    #[test]
    fn call_outside_a_span_starts_a_new_trace() {
        let spans = InMemorySpanExporter::default();
        let tracer_provider = SdkTracerProvider::builder()
            .with_simple_exporter(spans.clone())
            .build();
        let spicedb = DenyingSpiceDb::default();
        let address = serve(spicedb.clone());
        let mut config = config(&address.to_string(), true);
        config.timeout = Duration::from_secs(2);
        let client = SpiceDbClient::connect_with_telemetry(
            &config,
            ClientTelemetry::new(
                BoxedTracer::new(Box::new(tracer_provider.tracer(rpc::INSTRUMENTATION))),
                RpcMetrics::global(),
            ),
        )
        .expect("client");
        assert!(!Context::current().span().span_context().is_valid());

        let _ = client.read_page("delivery", "courier", None, 10);

        let finished = spans.get_finished_spans().expect("spans");
        assert_eq!(finished.len(), 1);
        assert_eq!(finished[0].name, READ_RELATIONSHIPS);
        assert_eq!(
            finished[0].parent_span_id,
            opentelemetry::trace::SpanId::INVALID
        );
    }
}
