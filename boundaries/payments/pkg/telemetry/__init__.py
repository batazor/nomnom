"""OpenTelemetry pipeline of a payments process: traces, metrics and JSON logs (ADR 9).

``setup`` registers the W3C propagator and, per signal enabled in the configuration file,
an OTLP exporter over ``spec.telemetry.protocol`` (``grpc`` or ``http/protobuf``) to
``spec.telemetry.endpoint``:

* traces — ``TracerProvider`` with a bounded ``BatchSpanProcessor``, only when
  ``spec.tracing.enabled`` (PostgreSQL statements are traced by the cursor of the pool,
  ``infrastructure/repository/statements.py``);
* metrics — ``MeterProvider`` with a periodic OTLP reader every
  ``spec.metrics.exportInterval`` and the CPython runtime metrics, only when
  ``spec.metrics.enabled``.

Both providers share one resource (``service.namespace=nomnom``, ``service.name`` of the
process, ``service.instance.id`` from ``POD_NAME``, ``spec.telemetry.resourceAttributes``);
JSON log records repeat its identity. Endpoint, protocol, resource, interval and the
processor limits are passed to the SDK explicitly, so ``OTEL_*`` variables do not change
them. With both flags off instruments stay no-op and nothing is exported.
"""

import logging
import threading
from collections.abc import Callable, Sequence
from datetime import timedelta
from typing import override
from urllib.parse import urlparse

from opentelemetry import metrics, trace
from opentelemetry.context import Context
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import MetricExporter, MetricReader
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import SpanProcessor, TracerProvider
from opentelemetry.sdk.trace.export import SpanExporter
from opentelemetry.sdk.trace.sampling import (
    Decision,
    ParentBased,
    Sampler,
    SamplingResult,
)
from opentelemetry.trace import Link, SpanKind
from opentelemetry.util.types import Attributes

from pkg.config import LogLevel, TelemetrySettings

from .buckets import SECONDS_BUCKETS
from .logs import JsonFormatter, configure_logging, redact
from .propagation import (
    BAGGAGE_ALLOWLIST,
    current_traceparent,
    install_propagator,
)
from .resource import SERVICE_NAMESPACE, build_resource, identity
from .runtime import register_runtime_metrics

__all__ = [
    "BAGGAGE_ALLOWLIST",
    "SECONDS_BUCKETS",
    "SERVICE_NAMESPACE",
    "JsonFormatter",
    "Telemetry",
    "build_resource",
    "configure_logging",
    "configure_process_logging",
    "create_meter_provider",
    "create_metric_exporter",
    "create_span_exporter",
    "create_tracer_provider",
    "current_traceparent",
    "identity",
    "redact",
    "setup",
]

logger = logging.getLogger(__name__)

# Уровни spec.logging.level.
LOG_LEVELS: dict[LogLevel, int] = {
    "debug": logging.DEBUG,
    "info": logging.INFO,
    "warn": logging.WARNING,
    "error": logging.ERROR,
}

# Границы очереди span processor: при недоступном collector теряются spans, а не память.
MAX_QUEUED_SPANS = 2048
MAX_EXPORT_BATCH = 512
SPAN_SCHEDULE_DELAY_MS = 5000
# Один export (с retries exporter) укладывается в shutdown.telemetryFlushTimeout (5s).
EXPORT_TIMEOUT_S = 3.0
SPAN_EXPORT_TIMEOUT_MS = 3_000
METRIC_EXPORT_TIMEOUT_MS = 3_000
# Пути сигналов OTLP/HTTP относительно spec.telemetry.endpoint.
HTTP_TRACES_PATH = "v1/traces"
HTTP_METRICS_PATH = "v1/metrics"


class Telemetry:
    """Owner of the process telemetry providers.

    ``shutdown()`` is the last step of the runtime contract
    (docs/cloud-native-runtime.md#observability-lifecycle).
    """

    def __init__(
        self,
        shutdowns: list[Callable[[], object]] | None = None,
        resource: Resource | None = None,
    ) -> None:
        self._shutdowns = shutdowns or []
        self.resource = resource or Resource.get_empty()

    @property
    def enabled(self) -> bool:
        """Whether OTLP export was configured and providers are installed."""
        return bool(self._shutdowns)

    def shutdown(self, timeout: float) -> bool:
        """Flush and shut down the providers within ``timeout`` seconds.

        Returns:
            ``False`` when the flush did not finish in time: an exporter may hang on an
            unreachable collector, and telemetry never delays the process exit.
        """
        if not self._shutdowns:
            return True
        done = threading.Event()

        def run() -> None:
            try:
                for shutdown in self._shutdowns:
                    try:
                        shutdown()
                    except Exception:
                        logger.exception("telemetry shutdown failed")
            finally:
                done.set()

        # Daemon: зависший exporter не блокирует выход интерпретатора.
        threading.Thread(target=run, name="telemetry-shutdown", daemon=True).start()
        if done.wait(timeout):
            return True
        logger.warning("telemetry flush did not finish within %.3fs", timeout)
        return False


class SkipOrphanClientSpans(Sampler):
    """Root sampler: a client call outside any operation is not traced.

    Pool checks would otherwise each start a one-span trace. Client spans inside a
    server or use case span follow their parent (``ParentBased``).
    """

    @override
    def should_sample(
        self,
        parent_context: Context | None,
        trace_id: int,
        name: str,
        kind: SpanKind | None = None,
        attributes: Attributes = None,
        links: Sequence[Link] | None = None,
        trace_state: trace.TraceState | None = None,
    ) -> SamplingResult:
        """Drop root client spans, sample every other root span."""
        if kind is SpanKind.CLIENT:
            return SamplingResult(Decision.DROP, None, trace_state)
        return SamplingResult(Decision.RECORD_AND_SAMPLE, attributes, trace_state)

    @override
    def get_description(self) -> str:
        """Name of the sampler in SDK diagnostics."""
        return "SkipOrphanClientSpans"


def create_tracer_provider(resource: Resource, processor: SpanProcessor) -> TracerProvider:
    """Tracer provider of the process with ``processor`` (tests pass an in-memory one)."""
    provider = TracerProvider(resource=resource, sampler=ParentBased(root=SkipOrphanClientSpans()))
    provider.add_span_processor(processor)
    return provider


def create_meter_provider(resource: Resource, readers: Sequence[MetricReader]) -> MeterProvider:
    """Meter provider of the process with ``readers`` and the CPython runtime metrics."""
    provider = MeterProvider(resource=resource, metric_readers=list(readers))
    register_runtime_metrics(provider.get_meter("nomnom.payments.runtime"))
    return provider


def setup(settings: TelemetrySettings) -> Telemetry:
    """Register the propagator and the OTLP providers of the enabled signals.

    Returns:
        A ``Telemetry`` owning the registered providers; disabled when neither
        ``spec.tracing.enabled`` nor ``spec.metrics.enabled`` is set.
    """
    install_propagator()
    resource = build_resource(settings)
    shutdowns: list[Callable[[], object]] = []
    if settings.tracing_enabled:
        shutdowns.extend(_setup_traces(resource, create_span_exporter(settings)))
    if settings.metrics_enabled:
        shutdowns.extend(
            _setup_metrics(
                resource, create_metric_exporter(settings), settings.metrics_export_interval
            )
        )
    if shutdowns:
        logger.info("telemetry exporting via OTLP %s to %s", settings.protocol, settings.endpoint)
    return Telemetry(shutdowns, resource)


def create_span_exporter(settings: TelemetrySettings) -> SpanExporter:
    """OTLP span exporter of ``spec.telemetry.protocol`` to ``spec.telemetry.endpoint``."""
    match settings.protocol:
        case "grpc":
            from grpc import Compression
            from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter

            return OTLPSpanExporter(
                endpoint=settings.endpoint,
                insecure=_plaintext(settings.endpoint),
                timeout=EXPORT_TIMEOUT_S,
                compression=Compression.NoCompression,
            )
        case "http/protobuf":
            from opentelemetry.exporter.otlp.proto.http import Compression
            from opentelemetry.exporter.otlp.proto.http.trace_exporter import (
                OTLPSpanExporter,
            )

            return OTLPSpanExporter(
                endpoint=_signal_url(settings.endpoint, HTTP_TRACES_PATH),
                timeout=EXPORT_TIMEOUT_S,
                compression=Compression.NoCompression,
            )


def create_metric_exporter(settings: TelemetrySettings) -> MetricExporter:
    """OTLP metric exporter of ``spec.telemetry.protocol`` to ``spec.telemetry.endpoint``."""
    match settings.protocol:
        case "grpc":
            from grpc import Compression
            from opentelemetry.exporter.otlp.proto.grpc.metric_exporter import (
                OTLPMetricExporter,
            )

            return OTLPMetricExporter(
                endpoint=settings.endpoint,
                insecure=_plaintext(settings.endpoint),
                timeout=EXPORT_TIMEOUT_S,
                compression=Compression.NoCompression,
            )
        case "http/protobuf":
            from opentelemetry.exporter.otlp.proto.http import Compression
            from opentelemetry.exporter.otlp.proto.http.metric_exporter import (
                OTLPMetricExporter,
            )

            return OTLPMetricExporter(
                endpoint=_signal_url(settings.endpoint, HTTP_METRICS_PATH),
                timeout=EXPORT_TIMEOUT_S,
                compression=Compression.NoCompression,
            )


def _plaintext(endpoint: str) -> bool:
    """GRPC without TLS for an ``http://`` endpoint, as the OTLP exporter decides by scheme."""
    return urlparse(endpoint).scheme == "http"


def _signal_url(endpoint: str, path: str) -> str:
    """URL of one OTLP/HTTP signal: the base endpoint plus ``v1/traces`` or ``v1/metrics``."""
    return f"{endpoint.rstrip('/')}/{path}"


def _setup_traces(resource: Resource, exporter: SpanExporter) -> list[Callable[[], object]]:
    from opentelemetry.sdk.trace.export import BatchSpanProcessor

    provider = create_tracer_provider(
        resource,
        BatchSpanProcessor(
            exporter,
            max_queue_size=MAX_QUEUED_SPANS,
            max_export_batch_size=MAX_EXPORT_BATCH,
            schedule_delay_millis=SPAN_SCHEDULE_DELAY_MS,
            export_timeout_millis=SPAN_EXPORT_TIMEOUT_MS,
        ),
    )
    trace.set_tracer_provider(provider)
    return [provider.shutdown]


def _setup_metrics(
    resource: Resource, exporter: MetricExporter, interval: timedelta
) -> list[Callable[[], object]]:
    from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader

    provider = create_meter_provider(
        resource,
        [
            PeriodicExportingMetricReader(
                exporter,
                export_interval_millis=interval.total_seconds() * 1000,
                export_timeout_millis=METRIC_EXPORT_TIMEOUT_MS,
            )
        ],
    )
    metrics.set_meter_provider(provider)
    return [provider.shutdown]


def configure_process_logging(settings: TelemetrySettings) -> None:
    """JSON logs on stdout at ``spec.logging.level`` with the identity of the process resource.

    Called by the entrypoint once the configuration is loaded; before that it logs through
    ``configure_logging`` with the process name only, so startup errors are JSON too.
    """
    configure_logging(identity(build_resource(settings)), LOG_LEVELS[settings.log_level])
