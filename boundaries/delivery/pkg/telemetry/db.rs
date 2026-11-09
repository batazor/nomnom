//! PostgreSQL client spans by the database semantic conventions: one
//! `SpanKind::Client` span per statement, named `{db.operation.name}
//! {db.collection.name}`, with `db.system.name` = `postgresql`,
//! `db.operation.name`, `db.collection.name` when the statement names one
//! table, `db.namespace` (the database of the store), `db.query.text` and on
//! failure `error.type` / `db.response.status_code` (SQLSTATE). The statement
//! text is parameterized (`$n`, values are bound), so it carries no data and
//! lets Database Observability match the span to the query; bind parameters
//! are never recorded.
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

/// One statement: its low-cardinality summary and its text.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct Statement {
    pub operation: Option<&'static str>,
    pub collection: Option<String>,
    /// `db.query.text`: the statement as sent to the server.
    pub text: String,
}

impl Statement {
    /// Statement of the first string literal of `source`: the call site as
    /// `stringify!` renders it (a `sqlx::query!` family macro call, plain or
    /// raw literal), or the statement itself.
    #[must_use]
    pub fn of_source(source: &str) -> Self {
        match first_literal(source) {
            Some(literal) => Self {
                text: literal.text(),
                ..Self::summary(literal.body)
            },
            None => Self::of_sql(source),
        }
    }

    #[must_use]
    pub fn of_sql(sql: &str) -> Self {
        Self {
            text: sql.to_owned(),
            ..Self::summary(sql)
        }
    }

    /// Operation keyword and table of `sql`, without the text.
    fn summary(sql: &str) -> Self {
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
            text: String::new(),
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

/// A string literal of Rust source: its body between the quotes and whether
/// it is raw (`r#"..."#`, no escapes).
struct Literal<'a> {
    body: &'a str,
    raw: bool,
}

impl Literal<'_> {
    /// The string value of the literal: a raw body as is, a plain body with
    /// its escapes resolved (`\"`, `\\`, `\n`, `\t`, `\r`, `\0`, `\'` and a
    /// line continuation `\` + newline + leading whitespace).
    fn text(&self) -> String {
        if self.raw {
            return self.body.to_owned();
        }
        let mut text = String::with_capacity(self.body.len());
        let mut chars = self.body.chars().peekable();
        while let Some(c) = chars.next() {
            if c != '\\' {
                text.push(c);
                continue;
            }
            match chars.next() {
                Some('n') => text.push('\n'),
                Some('t') => text.push('\t'),
                Some('r') => text.push('\r'),
                Some('0') => text.push('\0'),
                Some('\n') => while chars.next_if(|next| next.is_whitespace()).is_some() {},
                Some(escaped @ ('\\' | '"' | '\'')) => text.push(escaped),
                Some(other) => {
                    text.push('\\');
                    text.push(other);
                }
                None => text.push('\\'),
            }
        }
        text
    }
}

/// The first string literal of Rust `source`: `"..."` up to the first
/// unescaped quote, `r#"..."#` up to its closing quote and hashes (column
/// overrides such as `AS "courier_id!"` keep their quotes). An unterminated
/// literal runs to the end of `source`.
fn first_literal(source: &str) -> Option<Literal<'_>> {
    let open = source.find('"')?;
    let prefix = &source[..open];
    let hashes = prefix.len() - prefix.trim_end_matches('#').len();
    let prefix = &prefix[..prefix.len() - hashes];
    let raw = prefix.strip_suffix('r').is_some_and(|before| {
        !before
            .chars()
            .next_back()
            .is_some_and(|c| c.is_alphanumeric() || c == '_')
    });
    let body = &source[open + 1..];
    let end = if raw {
        body.find(&format!("\"{}", "#".repeat(hashes)))
    } else {
        let mut escaped = false;
        body.char_indices()
            .find(|&(_, c)| {
                let quote = c == '"' && !escaped;
                escaped = c == '\\' && !escaped;
                quote
            })
            .map(|(index, _)| index)
    };
    Some(Literal {
        body: end.map_or(body, |end| &body[..end]),
        raw,
    })
}

/// Starts the client span of `statement` under the current context; `None`
/// outside a trace. `namespace` is the database of the connection.
#[must_use]
pub fn start(
    tracer: &BoxedTracer,
    namespace: Option<&str>,
    statement: &Statement,
) -> Option<Context> {
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
    if let Some(namespace) = namespace {
        attributes.push(KeyValue::new("db.namespace", namespace.to_owned()));
    }
    if !statement.text.is_empty() {
        attributes.push(KeyValue::new("db.query.text", statement.text.clone()));
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
