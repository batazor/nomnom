//! Trace context of a gRPC command reaches its Outbox row (ADR 13): the
//! `delivery` server span continues the incoming `traceparent`, and the
//! Integration Event written in the same Unit of Work stores the server
//! span's `traceparent` in its `traceparent` column (ADR 37). PostgreSQL
//! comes from testcontainers (`tests/support`).

mod support;

use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

use opentelemetry::global::BoxedTracer;
use opentelemetry::trace::{SpanKind, TracerProvider as _};
use opentelemetry_sdk::trace::{InMemorySpanExporter, SdkTracerProvider};
use tokio::sync::watch;
use tonic::metadata::{MetadataMap, MetadataValue};

use delivery::di;
use delivery::infrastructure::grpc::v1::delivery_service_client::DeliveryServiceClient;
use delivery::infrastructure::grpc::v1::{CancelDeliveryRequest, CreateDeliveryRequest};
use delivery::infrastructure::grpc::{GrpcTraceLayer, GrpcWorker, SubjectResolver};
use delivery::infrastructure::memory::StaticAuthorization;
use delivery::pkg::authz::Subject;
use delivery::pkg::lifecycle::Worker as _;

const TRACE_ID: &str = "0af7651916cd43dd8448eb211c80319c";
const INCOMING: &str = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01";

struct OrdersWorker;

impl SubjectResolver for OrdersWorker {
    fn resolve(&self, _metadata: &MetadataMap) -> Result<Subject, tonic::Status> {
        Ok(Subject::Service("orders-worker".to_owned()))
    }
}

fn traced<T>(message: T) -> tonic::Request<T> {
    let mut request = tonic::Request::new(message);
    request
        .metadata_mut()
        .insert("traceparent", MetadataValue::from_static(INCOMING));
    request
}

#[tokio::test(flavor = "multi_thread")]
async fn command_with_traceparent_writes_it_to_the_outbox_row() {
    let config = support::database();
    let store = Arc::new(di::store(&config).expect("store"));
    let module = Arc::new(di::build_over(
        di::ProcessStores::over(&store),
        Box::new(StaticAuthorization::allow_all()),
    ));

    let exporter = InMemorySpanExporter::default();
    let provider = SdkTracerProvider::builder()
        .with_simple_exporter(exporter.clone())
        .build();
    let worker = GrpcWorker::bind(([127, 0, 0, 1], 0).into(), module, Arc::new(OrdersWorker))
        .expect("bind")
        .with_tracing(GrpcTraceLayer::new(BoxedTracer::new(Box::new(
            provider.tracer("test"),
        ))));
    let addr = worker.local_addr().expect("addr");
    let (stop, stopped) = watch::channel(false);
    let running = tokio::spawn(worker.run(stopped));
    let mut client = DeliveryServiceClient::connect(format!("http://{addr}"))
        .await
        .expect("connect");

    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    let order_id = format!("order-trace-{nanos}");
    let created = client
        .create_delivery(traced(CreateDeliveryRequest {
            order_id: order_id.clone(),
            market_id: "berlin".to_owned(),
        }))
        .await
        .expect("create")
        .into_inner();
    let cancelled = client
        .cancel_delivery(traced(CancelDeliveryRequest {
            order_id,
            reason: "customer request".to_owned(),
        }))
        .await
        .expect("cancel")
        .into_inner();
    assert!(cancelled.cancelled);

    let db = sqlx::PgPool::connect(&config.uri).await.expect("pool");
    let traceparents: Vec<String> = sqlx::query_scalar(
        "SELECT traceparent FROM delivery_outbox
         WHERE aggregate_id = $1 AND event_type = 'nomnom.delivery.v1.DeliveryCancelled'",
    )
    .bind(&created.delivery_id)
    .fetch_all(&db)
    .await
    .expect("outbox rows");
    assert_eq!(traceparents.len(), 1, "one cancellation event");
    let traceparent = traceparents[0].clone();

    provider.force_flush().expect("flush");
    let spans = exporter.get_finished_spans().expect("spans");
    let cancel = spans
        .iter()
        .find(|span| span.name == "nomnom.delivery.v1.DeliveryService/CancelDelivery")
        .expect("cancel server span");
    assert_eq!(cancel.span_kind, SpanKind::Server);
    assert_eq!(cancel.span_context.trace_id().to_string(), TRACE_ID);
    // Same trace as the caller, and the parent is the command's server span.
    assert_eq!(
        traceparent,
        format!("00-{TRACE_ID}-{}-01", cancel.span_context.span_id())
    );

    stop.send(true).expect("stop");
    running.await.expect("join").expect("shutdown");
    sqlx::query("DELETE FROM delivery_outbox WHERE aggregate_id = $1")
        .bind(&created.delivery_id)
        .execute(&db)
        .await
        .expect("cleanup");
}
