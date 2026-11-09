import logging
import os
import threading
import time
import unittest
from dataclasses import replace
from unittest.mock import patch

from opentelemetry.exporter.otlp.proto.grpc.metric_exporter import (
    OTLPMetricExporter as GrpcMetricExporter,
)
from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import (
    OTLPSpanExporter as GrpcSpanExporter,
)
from opentelemetry.exporter.otlp.proto.http.metric_exporter import (
    OTLPMetricExporter as HttpMetricExporter,
)
from opentelemetry.exporter.otlp.proto.http.trace_exporter import (
    OTLPSpanExporter as HttpSpanExporter,
)
from payment_fakes import TELEMETRY

from pkg import telemetry
from pkg.config import LogLevel

# Переменные OpenTelemetry SDK, которые процесс не читает (FR-765).
OTEL_ENV = {
    "OTEL_EXPORTER_OTLP_ENDPOINT": "http://other-collector:4318",
    "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://other-collector:4318/v1/traces",
    "OTEL_SERVICE_NAME": "other",
    "OTEL_RESOURCE_ATTRIBUTES": "service.name=other,service.instance.id=other-pod",
}


class TelemetryTest(unittest.TestCase):
    def test_disabled_without_flags(self) -> None:
        providers = telemetry.setup(TELEMETRY)
        self.assertFalse(providers.enabled)
        self.assertTrue(providers.shutdown(0.01))

    def test_endpoint_without_flags_exports_nothing(self) -> None:
        # AC-1080: endpoint не включает экспорт, его включают tracing.enabled и metrics.enabled.
        providers = telemetry.setup(replace(TELEMETRY, endpoint="http://127.0.0.1:1"))
        self.assertFalse(providers.enabled)

    def test_shutdown_runs_all_providers(self) -> None:
        calls: list[str] = []

        def failing() -> None:
            calls.append("traces")
            raise RuntimeError("collector unavailable")

        providers = telemetry.Telemetry([failing, lambda: calls.append("metrics")])
        self.assertTrue(providers.shutdown(1.0))
        self.assertEqual(calls, ["traces", "metrics"])

    def test_shutdown_bounded_by_timeout(self) -> None:
        release = threading.Event()
        providers = telemetry.Telemetry([lambda: release.wait(5)])
        started = time.monotonic()
        try:
            self.assertFalse(providers.shutdown(0.1))
            self.assertLess(time.monotonic() - started, 1.0)
        finally:
            release.set()

    def test_setup_with_tracing_registers_providers(self) -> None:
        providers = telemetry.setup(
            replace(TELEMETRY, endpoint="http://127.0.0.1:1", tracing_enabled=True)
        )
        self.assertTrue(providers.enabled)
        self.assertTrue(providers.shutdown(5.0))


class ExporterTest(unittest.TestCase):
    def test_grpc_exporters(self) -> None:
        settings = replace(TELEMETRY, endpoint="http://alloy:4317", protocol="grpc")
        spans = telemetry.create_span_exporter(settings)
        metrics = telemetry.create_metric_exporter(settings)
        self.addCleanup(spans.shutdown)
        self.addCleanup(metrics.shutdown)
        self.assertIsInstance(spans, GrpcSpanExporter)
        self.assertIsInstance(metrics, GrpcMetricExporter)

    def test_http_exporters_post_to_the_signal_paths(self) -> None:
        settings = replace(TELEMETRY, endpoint="http://alloy:4318/", protocol="http/protobuf")
        spans = telemetry.create_span_exporter(settings)
        metrics = telemetry.create_metric_exporter(settings)
        assert isinstance(spans, HttpSpanExporter)
        assert isinstance(metrics, HttpMetricExporter)
        self.assertEqual(spans._endpoint, "http://alloy:4318/v1/traces")
        self.assertEqual(metrics._endpoint, "http://alloy:4318/v1/metrics")

    def test_otel_environment_is_ignored(self) -> None:
        # AC-1079: endpoint, service.name и resource задаёт файл, а не OTEL_*.
        settings = replace(TELEMETRY, endpoint="http://alloy:4318", protocol="http/protobuf")
        with patch.dict(os.environ, OTEL_ENV):
            spans = telemetry.create_span_exporter(settings)
            resource = telemetry.build_resource(settings)
        assert isinstance(spans, HttpSpanExporter)
        self.assertEqual(spans._endpoint, "http://alloy:4318/v1/traces")
        self.assertEqual(resource.attributes["service.name"], "payments")
        self.assertEqual(resource.attributes["service.instance.id"], "payments-7d9f")
        self.assertEqual(resource.attributes["service.version"], "1.2.3")


class ProcessLoggingTest(unittest.TestCase):
    def test_level_comes_from_the_file(self) -> None:
        root = logging.getLogger()
        handlers, level = list(root.handlers), root.level

        def restore() -> None:
            for handler in list(root.handlers):
                root.removeHandler(handler)
            for handler in handlers:
                root.addHandler(handler)
            root.setLevel(level)

        self.addCleanup(restore)
        levels: tuple[tuple[LogLevel, int], ...] = (
            ("debug", logging.DEBUG),
            ("info", logging.INFO),
            ("warn", logging.WARNING),
            ("error", logging.ERROR),
        )
        for name, expected in levels:
            with self.subTest(level=name):
                telemetry.configure_process_logging(replace(TELEMETRY, log_level=name))
                self.assertEqual(root.level, expected)


if __name__ == "__main__":
    unittest.main()
