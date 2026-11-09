//! AC-1079: OpenTelemetry настраивается только файлом конфигурации — spans
//! уходят на `telemetry.endpoint` с `service.name` из файла, даже когда в
//! окружении процесса заданы `OTEL_*` другого коллектора.
//!
//! Один тест в собственном бинарнике: он меняет env процесса.

use std::collections::BTreeMap;
use std::io::{BufRead as _, BufReader, Read as _, Write as _};
use std::net::TcpListener;
use std::sync::mpsc;
use std::time::Duration;

use opentelemetry::trace::{Tracer as _, TracerProvider as _};

use delivery::pkg::config::{LogLevel, OtlpProtocol, TelemetryConfig};
use delivery::pkg::telemetry;

#[test]
fn spans_go_to_the_configured_endpoint_despite_otel_env() {
    let collector = TcpListener::bind("127.0.0.1:0").expect("bind collector");
    let other = TcpListener::bind("127.0.0.1:0").expect("bind other collector");
    let other_url = format!("http://{}", other.local_addr().expect("other address"));
    // SAFETY: единственный тест бинарника, другие потоки env не читают.
    unsafe {
        std::env::set_var("OTEL_EXPORTER_OTLP_ENDPOINT", &other_url);
        std::env::set_var(
            "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
            format!("{other_url}/v1/traces"),
        );
        std::env::set_var("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc");
        std::env::set_var("OTEL_SERVICE_NAME", "other-service");
        std::env::set_var(
            "OTEL_RESOURCE_ATTRIBUTES",
            "service.name=other-service,deployment.environment.name=other",
        );
    }
    let (received, requests) = mpsc::channel();
    let address = collector.local_addr().expect("collector address");
    std::thread::spawn(move || {
        let (stream, _) = collector.accept().expect("accept export");
        let mut reader = BufReader::new(stream);
        let mut request_line = String::new();
        reader.read_line(&mut request_line).expect("request line");
        let mut length = 0;
        loop {
            let mut line = String::new();
            reader.read_line(&mut line).expect("header");
            if line.trim().is_empty() {
                break;
            }
            if let Some((name, value)) = line.split_once(':')
                && name.eq_ignore_ascii_case("content-length")
            {
                length = value.trim().parse().expect("content length");
            }
        }
        let mut body = vec![0; length];
        reader.read_exact(&mut body).expect("body");
        reader
            .get_mut()
            .write_all(b"HTTP/1.1 200 OK\r\ncontent-type: application/x-protobuf\r\ncontent-length: 0\r\n\r\n")
            .expect("respond");
        let _ = received.send((request_line, body));
    });

    let pipeline = telemetry::start(&TelemetryConfig {
        service_name: "delivery-worker".to_owned(),
        instance_id: "delivery-worker-7d9f".to_owned(),
        endpoint: Some(format!("http://{address}")),
        protocol: OtlpProtocol::HttpProtobuf,
        resource_attributes: BTreeMap::from([(
            "deployment.environment.name".to_owned(),
            "kind".to_owned(),
        )]),
        tracing: true,
        metrics: false,
        metrics_export_interval: Duration::from_secs(60),
        log_level: LogLevel::Info,
    })
    .expect("start telemetry");
    assert!(pipeline.exporting_traces());
    assert!(!pipeline.exporting());
    opentelemetry::global::tracer_provider()
        .tracer("telemetry-export-test")
        .in_span("probe", |_| {});
    pipeline.shutdown();

    let (request_line, body) = requests
        .recv_timeout(Duration::from_secs(10))
        .expect("spans exported to telemetry.endpoint");
    assert!(
        request_line.starts_with("POST /v1/traces "),
        "{request_line}"
    );
    let contains = |needle: &str| {
        body.windows(needle.len())
            .any(|part| part == needle.as_bytes())
    };
    assert!(contains("delivery-worker"));
    assert!(contains("delivery-worker-7d9f"));
    assert!(contains("probe"));
    assert!(contains("kind"));
    assert!(!contains("other-service"));
    other.set_nonblocking(true).expect("nonblocking");
    assert!(
        other.accept().is_err(),
        "nothing is exported to the OTEL_* endpoint"
    );
}
