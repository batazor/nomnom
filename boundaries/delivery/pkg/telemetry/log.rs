//! Structured logs of the process (docs/observability.md, «Логи»): one JSON
//! record per line on stdout. Code logs through `tracing` macros; [`JsonLayer`]
//! renders every event with the common schema:
//!
//! - `timestamp` (UTC, RFC 3339 with milliseconds), `severity`
//!   (`TRACE`..`ERROR`), `message`;
//! - `service.namespace`, `service.name`, `service.version`,
//!   `service.instance.id`, `deployment.environment.name` from the same
//!   OpenTelemetry resource as traces and metrics (absent attributes are
//!   omitted);
//! - `trace_id`, `span_id` of the active OpenTelemetry span, only when there
//!   is one;
//! - the event's own fields: `operation` (stable operation name),
//!   `outcome`, allowed lookup identifiers, `error.type` and a safe
//!   `error.message`.
//!
//! Redaction is the caller's duty: tokens, credentials, payloads, bind
//! parameters and Kafka message contents are never passed as fields. Library
//! targets log at `WARN` and above only (`sqlx` at `ERROR`: its slow-query
//! warnings carry statement text).

use std::fmt::{self, Write as _};
use std::io::Write as _;
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

use opentelemetry::trace::TraceContextExt as _;
use opentelemetry_sdk::Resource;
use serde_json::{Map, Value};
use tracing::field::{Field, Visit};
use tracing::{Event, Level, Subscriber};
use tracing_subscriber::Layer;
use tracing_subscriber::filter::{LevelFilter, Targets};
use tracing_subscriber::layer::{Context as LayerContext, SubscriberExt as _};

/// Resource attributes copied into every record.
const RESOURCE_FIELDS: [&str; 5] = [
    "service.namespace",
    "service.name",
    "service.version",
    "service.instance.id",
    "deployment.environment.name",
];

/// Destination of one serialized record.
pub type LogWriter = Arc<dyn Fn(&str) + Send + Sync>;

/// Current time of a record.
pub type LogClock = Arc<dyn Fn() -> SystemTime + Send + Sync>;

/// `tracing` layer that writes each event as one JSON line.
pub struct JsonLayer {
    resource: Map<String, Value>,
    write: LogWriter,
    now: LogClock,
}

impl JsonLayer {
    #[must_use]
    pub fn new(resource: &Resource, write: LogWriter, now: LogClock) -> Self {
        let resource = RESOURCE_FIELDS
            .iter()
            .filter_map(|key| {
                resource
                    .get(&opentelemetry::Key::from_static_str(key))
                    .map(|value| {
                        (
                            (*key).to_owned(),
                            Value::String(value.as_str().into_owned()),
                        )
                    })
            })
            .collect();
        Self {
            resource,
            write,
            now,
        }
    }

    /// Writes to stdout, one locked write per record.
    #[must_use]
    pub fn stdout(resource: &Resource) -> Self {
        Self::new(
            resource,
            Arc::new(|line| {
                let mut stdout = std::io::stdout().lock();
                // A closed stdout must not fail the business operation.
                let _ = writeln!(stdout, "{line}");
            }),
            Arc::new(SystemTime::now),
        )
    }

    /// One record of `event` as a JSON line.
    fn render(&self, event: &Event<'_>) -> String {
        let mut record = Map::new();
        record.insert("timestamp".to_owned(), rfc3339((self.now)()).into());
        record.insert(
            "severity".to_owned(),
            severity(*event.metadata().level()).into(),
        );
        let mut fields = Fields::default();
        event.record(&mut fields);
        record.insert(
            "message".to_owned(),
            fields.message.unwrap_or_default().into(),
        );
        record.extend(self.resource.clone());
        let context = opentelemetry::Context::current();
        let span = context.span();
        let span_context = span.span_context();
        if span_context.is_valid() {
            record.insert(
                "trace_id".to_owned(),
                span_context.trace_id().to_string().into(),
            );
            record.insert(
                "span_id".to_owned(),
                span_context.span_id().to_string().into(),
            );
        }
        for (key, value) in fields.values {
            record.entry(key).or_insert(value);
        }
        Value::Object(record).to_string()
    }
}

impl<S: Subscriber> Layer<S> for JsonLayer {
    fn on_event(&self, event: &Event<'_>, _context: LayerContext<'_, S>) {
        (self.write)(&self.render(event));
    }
}

/// Process code at `INFO`, libraries at `WARN`, `sqlx` at `ERROR`.
#[must_use]
pub fn filter() -> Targets {
    Targets::new()
        .with_default(LevelFilter::WARN)
        .with_target(env!("CARGO_CRATE_NAME"), LevelFilter::INFO)
        .with_target("delivery_relay", LevelFilter::INFO)
        .with_target("delivery_migrate", LevelFilter::INFO)
        .with_target("sqlx", LevelFilter::ERROR)
}

/// Installs the JSON stdout logger as the global `tracing` subscriber. Called
/// first in `main`, before configuration is loaded, so startup errors share
/// the format. A second call keeps the first subscriber.
pub fn init_logs(default_service_name: &str) {
    let resource = super::resource(default_service_name);
    let subscriber =
        tracing_subscriber::registry().with(JsonLayer::stdout(&resource).with_filter(filter()));
    let _ = tracing::subscriber::set_global_default(subscriber);
}

/// Records logged by `work` on the current thread, rendered with `resource`
/// and a fixed clock (2026-10-25T12:34:56.789Z) — for tests of the log
/// format, including integration tests.
pub fn capture_json(resource: &Resource, work: impl FnOnce()) -> Vec<Value> {
    let lines = Arc::new(std::sync::Mutex::new(Vec::<String>::new()));
    let sink = Arc::clone(&lines);
    let layer = JsonLayer::new(
        resource,
        Arc::new(move |line| {
            if let Ok(mut lines) = sink.lock() {
                lines.push(line.to_owned());
            }
        }),
        Arc::new(|| UNIX_EPOCH + std::time::Duration::from_millis(1_792_931_696_789)),
    );
    let subscriber = tracing_subscriber::registry().with(layer.with_filter(filter()));
    tracing::subscriber::with_default(subscriber, work);
    let lines = lines.lock().map(|lines| lines.clone()).unwrap_or_default();
    lines
        .iter()
        .filter_map(|line| serde_json::from_str(line).ok())
        .collect()
}

fn severity(level: Level) -> &'static str {
    match level {
        Level::ERROR => "ERROR",
        Level::WARN => "WARN",
        Level::INFO => "INFO",
        Level::DEBUG => "DEBUG",
        Level::TRACE => "TRACE",
    }
}

/// Event fields: `message` separately, the rest in call order.
#[derive(Default)]
struct Fields {
    message: Option<String>,
    values: Vec<(String, Value)>,
}

impl Fields {
    fn put(&mut self, field: &Field, value: Value) {
        if field.name() == "message" {
            self.message = Some(match value {
                Value::String(text) => text,
                other => other.to_string(),
            });
        } else {
            self.values.push((field.name().to_owned(), value));
        }
    }
}

impl Visit for Fields {
    fn record_f64(&mut self, field: &Field, value: f64) {
        self.put(field, value.into());
    }

    fn record_i64(&mut self, field: &Field, value: i64) {
        self.put(field, value.into());
    }

    fn record_u64(&mut self, field: &Field, value: u64) {
        self.put(field, value.into());
    }

    fn record_bool(&mut self, field: &Field, value: bool) {
        self.put(field, value.into());
    }

    fn record_str(&mut self, field: &Field, value: &str) {
        self.put(field, value.into());
    }

    fn record_error(&mut self, field: &Field, value: &(dyn std::error::Error + 'static)) {
        self.put(field, value.to_string().into());
    }

    fn record_debug(&mut self, field: &Field, value: &dyn fmt::Debug) {
        let mut text = String::new();
        let _ = write!(text, "{value:?}");
        self.put(field, text.into());
    }
}

/// UTC `YYYY-MM-DDTHH:MM:SS.mmmZ`.
#[must_use]
pub fn rfc3339(time: SystemTime) -> String {
    let elapsed = time.duration_since(UNIX_EPOCH).unwrap_or_default();
    let seconds = i64::try_from(elapsed.as_secs()).unwrap_or(i64::MAX);
    let (days, of_day) = (seconds.div_euclid(86_400), seconds.rem_euclid(86_400));
    // Civil date from days since 1970-01-01 (H. Hinnant's algorithm).
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1_460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let day = doy - (153 * mp + 2) / 5 + 1;
    let month = if mp < 10 { mp + 3 } else { mp - 9 };
    let year = yoe + era * 400 + i64::from(month <= 2);
    format!(
        "{year:04}-{month:02}-{day:02}T{:02}:{:02}:{:02}.{:03}Z",
        of_day / 3_600,
        of_day % 3_600 / 60,
        of_day % 60,
        elapsed.subsec_millis()
    )
}

#[cfg(test)]
#[path = "log/tests.rs"]
mod tests;
