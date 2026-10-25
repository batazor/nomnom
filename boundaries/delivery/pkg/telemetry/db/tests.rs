use opentelemetry::trace::TracerProvider as _;
use opentelemetry_sdk::trace::{InMemorySpanExporter, SdkTracerProvider};

use super::*;

#[test]
fn statement_summary_has_operation_and_table_only() {
    assert_eq!(
        Statement::of_sql("SELECT version FROM deliveries WHERE id = $1"),
        Statement {
            operation: Some("SELECT"),
            collection: Some("deliveries".to_owned()),
        }
    );
    assert_eq!(
        Statement::of_sql(
            "INSERT INTO delivery_outbox (event_id, event_type) VALUES ($1::uuid, $2)"
        ),
        Statement {
            operation: Some("INSERT"),
            collection: Some("delivery_outbox".to_owned()),
        }
    );
    assert_eq!(
        Statement::of_sql("update couriers SET version = version + 1 WHERE id = $1"),
        Statement {
            operation: Some("UPDATE"),
            collection: Some("couriers".to_owned()),
        }
    );
    assert_eq!(Statement::of_sql("COMMIT").operation, Some("COMMIT"));
    assert_eq!(Statement::of_sql("VACUUM secrets"), Statement::default());
}

#[test]
fn call_site_source_yields_the_literal_statement() {
    let source = stringify!(
        sqlx::query_scalar!("SELECT version FROM deliveries WHERE id = $1", aggregate_id)
            .fetch_optional(connection)
    );
    assert_eq!(
        Statement::of_source(source).collection.as_deref(),
        Some("deliveries")
    );
    assert_eq!(
        Statement::of_source(stringify!(sqlx::query_as!(
            DeliveryRow,
            "SELECT id, order_id FROM deliveries WHERE order_id = $1",
            order_id.as_str()
        ))),
        Statement {
            operation: Some("SELECT"),
            collection: Some("deliveries".to_owned()),
        }
    );
}

#[test]
fn column_overrides_do_not_end_the_statement_literal() {
    let deliveries = Statement {
        operation: Some("SELECT"),
        collection: Some("deliveries".to_owned()),
    };
    assert_eq!(
        Statement::of_source(stringify!(sqlx::query!(
            r#"SELECT id, courier_id AS "courier_id!" FROM deliveries WHERE id = ANY($1)"#,
            ids
        ))),
        deliveries
    );
    assert_eq!(
        Statement::of_source(stringify!(sqlx::query!(
            "SELECT courier_id AS \"courier_id!\" FROM deliveries WHERE id = $1",
            id
        ))),
        deliveries
    );
    assert_eq!(
        Statement::of_source(stringify!(sqlx::query!(
            r##"SELECT id AS "id!" FROM delivery_offers WHERE note = '"#'"##
        )))
        .collection
        .as_deref(),
        Some("delivery_offers")
    );
}

#[test]
fn span_is_a_child_of_the_current_trace_and_has_no_statement_text() {
    let exporter = InMemorySpanExporter::default();
    let provider = SdkTracerProvider::builder()
        .with_simple_exporter(exporter.clone())
        .build();
    let tracer = BoxedTracer::new(Box::new(provider.tracer(INSTRUMENTATION)));
    let statement = Statement::of_sql("SELECT secret_column FROM deliveries WHERE id = $1");

    assert!(
        start(&tracer, &statement).is_none(),
        "no span outside a trace"
    );

    let parent = Context::new().with_span(tracer.start("usecase confirm_pickup"));
    {
        let _attached = parent.clone().attach();
        let context = start(&tracer, &statement);
        end::<()>(context, &Err(sqlx::Error::PoolTimedOut));
    }
    parent.span().end();

    let spans = exporter.get_finished_spans().expect("spans");
    let db = spans
        .iter()
        .find(|span| span.name == "SELECT deliveries")
        .expect("db span");
    assert_eq!(db.span_kind, SpanKind::Client);
    assert_eq!(db.parent_span_id, spans[1].span_context.span_id());
    let mut attributes: Vec<(String, String)> = db
        .attributes
        .iter()
        .map(|kv| (kv.key.to_string(), kv.value.as_str().into_owned()))
        .collect();
    attributes.sort();
    assert_eq!(
        attributes,
        vec![
            ("db.collection.name".to_owned(), "deliveries".to_owned()),
            ("db.operation.name".to_owned(), "SELECT".to_owned()),
            ("db.system.name".to_owned(), "postgresql".to_owned()),
            ("error.type".to_owned(), "pool_timed_out".to_owned()),
        ]
    );
}
