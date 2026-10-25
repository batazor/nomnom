"""OpenTelemetry pipeline of a payments process: traces, metrics and JSON logs (ADR 9).

``setup`` registers the W3C propagator and, per signal, an OTLP gRPC exporter:

* traces — ``TracerProvider`` with a bounded ``BatchSpanProcessor``, only when
  ``OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`` or ``OTEL_EXPORTER_OTLP_ENDPOINT`` is set;
  psycopg is instrumented without bind parameters;
* metrics — ``MeterProvider`` with a periodic OTLP reader and the CPython runtime
  metrics, only when ``OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`` or
  ``OTEL_EXPORTER_OTLP_ENDPOINT`` is set.

Both providers share one resource (``service.namespace=nomnom``, ``service.name`` of the
process, ``OTEL_RESOURCE_ATTRIBUTES``); JSON log records repeat its identity. Without an
endpoint instruments stay no-op and nothing is exported.
"""

import logging
import os
import threading
from collections.abc import Callable, Mapping, Sequence
from typing import override

from opentelemetry import metrics, trace
from opentelemetry.context import Context
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import MetricReader
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import SpanProcessor, TracerProvider
from opentelemetry.sdk.trace.sampling import (
    Decision,
    ParentBased,
    Sampler,
    SamplingResult,
)
from opentelemetry.trace import Link, SpanKind
from opentelemetry.util.types import Attributes

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
    "create_tracer_provider",
    "current_traceparent",
    "identity",
    "redact",
    "setup",
]

logger = logging.getLogger(__name__)

_TRACES_ENDPOINT_ENVS = ("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT")
_METRICS_ENDPOINT_ENVS = ("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT")

# Границы очереди span processor: при недоступном collector теряются spans, а не память.
MAX_QUEUED_SPANS = 2048
MAX_EXPORT_BATCH = 512
SPAN_SCHEDULE_DELAY_MS = 5000
# Один export (с retries exporter) укладывается в SHUTDOWN__TELEMETRY_FLUSH_TIMEOUT (5s).
EXPORT_TIMEOUT_S = 3.0
SPAN_EXPORT_TIMEOUT_MS = 3_000
METRIC_EXPORT_INTERVAL_MS = 60_000
METRIC_EXPORT_TIMEOUT_MS = 3_000


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


def setup(service_name: str = "payments", environ: Mapping[str, str] | None = None) -> Telemetry:
    """Register the propagator and the OTLP providers configured by the environment.

    Returns:
        A ``Telemetry`` owning the registered providers; disabled when no OTLP endpoint
        variable is set.
    """
    env = os.environ if environ is None else environ
    install_propagator()
    resource = build_resource(service_name, env)
    shutdowns: list[Callable[[], object]] = []
    traces_endpoint = _endpoint(env, _TRACES_ENDPOINT_ENVS)
    if traces_endpoint is not None:
        shutdowns.extend(_setup_traces(resource, traces_endpoint))
    metrics_endpoint = _endpoint(env, _METRICS_ENDPOINT_ENVS)
    if metrics_endpoint is not None:
        shutdowns.extend(_setup_metrics(resource, metrics_endpoint))
    if shutdowns:
        logger.info("telemetry exporting via OTLP")
    return Telemetry(shutdowns, resource)


def _endpoint(env: Mapping[str, str], names: Sequence[str]) -> str | None:
    """First set endpoint variable: the signal-specific one wins over the general one."""
    return next((value for name in names if (value := env.get(name, "").strip())), None)


def _setup_traces(resource: Resource, endpoint: str) -> list[Callable[[], object]]:
    from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
    from opentelemetry.instrumentation.psycopg import PsycopgInstrumentor
    from opentelemetry.sdk.trace.export import BatchSpanProcessor

    provider = create_tracer_provider(
        resource,
        BatchSpanProcessor(
            OTLPSpanExporter(endpoint=endpoint, timeout=EXPORT_TIMEOUT_S),
            max_queue_size=MAX_QUEUED_SPANS,
            max_export_batch_size=MAX_EXPORT_BATCH,
            schedule_delay_millis=SPAN_SCHEDULE_DELAY_MS,
            export_timeout_millis=SPAN_EXPORT_TIMEOUT_MS,
        ),
    )
    trace.set_tracer_provider(provider)
    # Спаны запросов PostgreSQL: statement — статический SQL с плейсхолдерами (значения
    # уходят отдельно, server-side binding psycopg 3), bind parameters не пишутся.
    instrumentor = PsycopgInstrumentor()
    instrumentor.instrument(
        tracer_provider=provider,
        capture_parameters=False,
        enable_commenter=False,
        skip_dep_check=True,
    )
    return [instrumentor.uninstrument, provider.shutdown]


def _setup_metrics(resource: Resource, endpoint: str) -> list[Callable[[], object]]:
    from opentelemetry.exporter.otlp.proto.grpc.metric_exporter import OTLPMetricExporter
    from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader

    provider = create_meter_provider(
        resource,
        [
            PeriodicExportingMetricReader(
                OTLPMetricExporter(endpoint=endpoint, timeout=EXPORT_TIMEOUT_S),
                export_interval_millis=METRIC_EXPORT_INTERVAL_MS,
                export_timeout_millis=METRIC_EXPORT_TIMEOUT_MS,
            )
        ],
    )
    metrics.set_meter_provider(provider)
    return [provider.shutdown]


def configure_process_logging(
    service_name: str, environ: Mapping[str, str] | None = None, level: int = logging.INFO
) -> None:
    """JSON logs on stdout with the identity of the process resource.

    Called by the entrypoint before anything else, so startup errors are JSON too.
    """
    env = os.environ if environ is None else environ
    configure_logging(identity(build_resource(service_name, env)), level)
