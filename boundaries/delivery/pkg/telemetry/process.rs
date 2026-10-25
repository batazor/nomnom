//! Process runtime metrics by OpenTelemetry semantic conventions (inventory
//! section «runtime», Rust): `process.cpu.time` (`s`, `cpu.mode` = `user` |
//! `system`), `process.memory.usage` (resident set, `By`) and
//! `process.thread.count`. Values are read from `/proc/self` at collection
//! time, so the instruments cost nothing between exports; on a platform
//! without procfs (local macOS runs) nothing is observed. Tokio runtime
//! metrics have no semantic conventions and are not exported.

use std::sync::Arc;

use opentelemetry::KeyValue;
use opentelemetry::metrics::Meter;

/// Instrumentation scope of the process metrics.
pub const METER_NAME: &str = "nomnom.delivery.process";

/// Clock ticks per second of `/proc/<pid>/stat` times (`USER_HZ`, 100 on
/// every Linux architecture the image targets).
const USER_HZ: f64 = 100.0;

/// One reading of the process counters.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct ProcessSample {
    pub cpu_user_seconds: f64,
    pub cpu_system_seconds: f64,
    pub memory_rss_bytes: i64,
    pub threads: i64,
}

/// Source of [`ProcessSample`]; `None` when the platform has no data.
pub type Sampler = Arc<dyn Fn() -> Option<ProcessSample> + Send + Sync>;

/// Registers the observable instruments on `meter`. They live as long as
/// the MeterProvider.
pub fn register(meter: &Meter, sampler: Sampler) {
    let cpu = Arc::clone(&sampler);
    meter
        .f64_observable_counter("process.cpu.time")
        .with_unit("s")
        .with_description("Total CPU seconds broken down by different CPU modes.")
        .with_callback(move |observer| {
            if let Some(sample) = cpu() {
                observer.observe(
                    sample.cpu_user_seconds,
                    &[KeyValue::new("cpu.mode", "user")],
                );
                observer.observe(
                    sample.cpu_system_seconds,
                    &[KeyValue::new("cpu.mode", "system")],
                );
            }
        })
        .build();
    let memory = Arc::clone(&sampler);
    meter
        .i64_observable_up_down_counter("process.memory.usage")
        .with_unit("By")
        .with_description("The amount of physical memory in use.")
        .with_callback(move |observer| {
            if let Some(sample) = memory() {
                observer.observe(sample.memory_rss_bytes, &[]);
            }
        })
        .build();
    meter
        .i64_observable_up_down_counter("process.thread.count")
        .with_unit("{thread}")
        .with_description("Process threads count.")
        .with_callback(move |observer| {
            if let Some(sample) = sampler() {
                observer.observe(sample.threads, &[]);
            }
        })
        .build();
}

/// Reads `/proc/self/stat` and `/proc/self/status`.
#[must_use]
pub fn procfs() -> Sampler {
    Arc::new(|| {
        let stat = std::fs::read_to_string("/proc/self/stat").ok()?;
        let status = std::fs::read_to_string("/proc/self/status").ok()?;
        parse(&stat, &status)
    })
}

/// Sample from the contents of `/proc/self/stat` and `/proc/self/status`.
#[must_use]
pub fn parse(stat: &str, status: &str) -> Option<ProcessSample> {
    // `comm` (field 2) may contain spaces; fields after it are fixed.
    let (_, rest) = stat.rsplit_once(')')?;
    let fields: Vec<&str> = rest.split_whitespace().collect();
    // `rest` starts at field 3 (state): utime is field 14, stime 15,
    // num_threads 20.
    let field = |number: usize| fields.get(number - 3).copied();
    let ticks = |number: usize| field(number)?.parse::<u64>().ok();
    let rss_kib = status.lines().find_map(|line| {
        line.strip_prefix("VmRSS:")?
            .trim()
            .strip_suffix("kB")?
            .trim()
            .parse::<i64>()
            .ok()
    })?;
    Some(ProcessSample {
        cpu_user_seconds: seconds(ticks(14)?),
        cpu_system_seconds: seconds(ticks(15)?),
        memory_rss_bytes: rss_kib.saturating_mul(1024),
        threads: field(20)?.parse().ok()?,
    })
}

#[allow(clippy::cast_precision_loss)]
fn seconds(ticks: u64) -> f64 {
    ticks as f64 / USER_HZ
}

#[cfg(test)]
#[path = "process/tests.rs"]
mod tests;
