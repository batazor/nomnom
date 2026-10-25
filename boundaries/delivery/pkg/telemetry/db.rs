//! PostgreSQL client spans by the database semantic conventions: one
//! `SpanKind::Client` span per statement, named `{db.operation.name}
//! {db.collection.name}`, with `db.system.name` = `postgresql`,
//! `db.operation.name`, `db.collection.name` when the statement names one
//! table, and on failure `error.type` / `db.response.status_code` (SQLSTATE).
//! Neither the statement text nor bind parameters are recorded: only the
//! operation keyword and the table come from the statement.
//!
//! Spans are created only inside an existing trace (a use case, a gRPC
//! call): a statement of a background loop without a parent would be a
//! one-span trace per poll, noise rather than diagnosis.

use opentelemetry::global::BoxedTracer;
use opentelemetry::trace::{SpanKind, Status, TraceContextExt as _, Tracer as _};
use opentelemetry::{Context, KeyValue};

/// Instrumentation scope of the database spans.
pub const INSTRUMENTATION: &str = "nomnom.delivery.postgres";

/// Operation keywords recorded as `db.operation.name`.
const OPERATIONS: [&str; 9] = [
    "SELECT", "INSERT", "UPDATE", "DELETE", "WITH", "BEGIN", "COMMIT", "ROLLBACK", "LOCK",
];

/// Low-cardinality summary of one statement.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct Statement {
    pub operation: Option<&'static str>,
    pub collection: Option<String>,
}

impl Statement {
    /// Summary of the first string literal of `source`: the statement text
    /// of a call site as `stringify!` renders it, or the statement itself.
    #[must_use]
    pub fn of_source(source: &str) -> Self {
        let text = source
            .split_once('"')
            .map_or(source, |(_, rest)| rest.split('"').next().unwrap_or(rest));
        Self::of_sql(text)
    }

    #[must_use]
    pub fn of_sql(sql: &str) -> Self {
        let words: Vec<String> = sql
            .split(|c: char| c.is_whitespace() || c == '(' || c == ')' || c == ',' || c == ';')
            .filter(|word| !word.is_empty())
            .map(str::to_ascii_uppercase)
            .collect();
        let Some(operation) = words
            .first()
            .and_then(|first| OPERATIONS.iter().find(|known| **known == first.as_str()))
            .copied()
        else {
            return Self::default();
        };
        let after = |keyword: &str| {
            words
                .iter()
                .position(|word| word == keyword)
                .and_then(|index| words.get(index + 1))
        };
        let collection = match operation {
            "SELECT" | "DELETE" => after("FROM"),
            "INSERT" => after("INTO"),
            "UPDATE" | "LOCK" => words
                .get(1)
                .filter(|word| *word != "TABLE")
                .or_else(|| words.get(2)),
            _ => None,
        }
        .map(|table| table.to_ascii_lowercase())
        .filter(|table| {
            table
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || c == '_' || c == '.')
        });
        Self {
            operation: Some(operation),
            collection,
        }
    }

    fn span_name(&self) -> String {
        match (self.operation, &self.collection) {
            (Some(operation), Some(collection)) => format!("{operation} {collection}"),
            (Some(operation), None) => operation.to_owned(),
            (None, _) => "postgresql".to_owned(),
        }
    }
}

/// Starts the client span of `statement` under the current context; `None`
/// outside a trace.
#[must_use]
pub fn start(tracer: &BoxedTracer, statement: &Statement) -> Option<Context> {
    let parent = Context::current();
    if !parent.span().span_context().is_valid() {
        return None;
    }
    let mut attributes = vec![KeyValue::new("db.system.name", "postgresql")];
    if let Some(operation) = statement.operation {
        attributes.push(KeyValue::new("db.operation.name", operation));
    }
    if let Some(collection) = &statement.collection {
        attributes.push(KeyValue::new("db.collection.name", collection.clone()));
    }
    let span = tracer
        .span_builder(statement.span_name())
        .with_kind(SpanKind::Client)
        .with_attributes(attributes)
        .start_with_context(tracer, &parent);
    Some(parent.with_span(span))
}

/// Ends the span of [`start`]; a failed statement records [`error_type`]
/// and the SQLSTATE, never the message (it may echo values).
pub fn end<T>(context: Option<Context>, result: &Result<T, sqlx::Error>) {
    let Some(context) = context else {
        return;
    };
    let span = context.span();
    if let Err(error) = result {
        if let Some(code) = error.as_database_error().and_then(|db| db.code()) {
            span.set_attribute(KeyValue::new("db.response.status_code", code.into_owned()));
        }
        let error_type = error_type(error);
        span.set_attribute(KeyValue::new("error.type", error_type.clone()));
        span.set_status(Status::error(error_type));
    }
    span.end();
}

/// `error.type` of a failed statement: the SQLSTATE of a database error,
/// otherwise a closed class.
#[must_use]
pub fn error_type(error: &sqlx::Error) -> String {
    match error {
        sqlx::Error::Database(database) => database
            .code()
            .map_or_else(|| "database".to_owned(), |code| code.into_owned()),
        sqlx::Error::PoolTimedOut => "pool_timed_out".to_owned(),
        sqlx::Error::PoolClosed => "pool_closed".to_owned(),
        sqlx::Error::Io(_) => "io".to_owned(),
        sqlx::Error::Tls(_) => "tls".to_owned(),
        sqlx::Error::Protocol(_) => "protocol".to_owned(),
        sqlx::Error::RowNotFound => "row_not_found".to_owned(),
        sqlx::Error::ColumnDecode { .. } | sqlx::Error::Decode(_) => "decode".to_owned(),
        _ => "_OTHER".to_owned(),
    }
}

#[cfg(test)]
#[path = "db/tests.rs"]
mod tests;
