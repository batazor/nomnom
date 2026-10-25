import logging
import os
import threading
from collections.abc import Callable, Mapping

logger = logging.getLogger(__name__)

# Экспорт включается стандартными переменными OpenTelemetry SDK (ADR 9): без endpoint
# процесс работает без telemetry, например локально и в black-box тестах.
_ENDPOINT_ENVS = ("OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")


class Telemetry:
    """Владелец telemetry providers процесса. shutdown() вызывается последним шагом
    runtime contract (docs/cloud-native-runtime.md, шаг 8)."""

    def __init__(self, shutdowns: list[Callable[[], object]] | None = None) -> None:
        self._shutdowns = shutdowns or []

    @property
    def enabled(self) -> bool:
        return bool(self._shutdowns)

    def shutdown(self, timeout: float) -> bool:
        """Flush и shutdown providers в пределах timeout секунд. Возвращает False,
        если не уложились: exporter мог зависнуть на недоступном collector, а выход
        процесса из-за telemetry не задерживается."""
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


def setup(environ: Mapping[str, str] | None = None) -> Telemetry:
    """Регистрирует глобальные tracer и meter providers с OTLP exporter."""
    env = os.environ if environ is None else environ
    if not any(env.get(name) for name in _ENDPOINT_ENVS):
        return Telemetry()

    from opentelemetry import metrics, trace
    from opentelemetry.exporter.otlp.proto.grpc.metric_exporter import OTLPMetricExporter
    from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter
    from opentelemetry.sdk.metrics import MeterProvider
    from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
    from opentelemetry.sdk.resources import Resource
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import BatchSpanProcessor

    # Resource.create() читает OTEL_SERVICE_NAME и OTEL_RESOURCE_ATTRIBUTES.
    resource = Resource.create({"service.name": env.get("OTEL_SERVICE_NAME", "payments")})

    tracer_provider = TracerProvider(resource=resource)
    tracer_provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
    trace.set_tracer_provider(tracer_provider)

    meter_provider = MeterProvider(
        resource=resource,
        metric_readers=[PeriodicExportingMetricReader(OTLPMetricExporter())],
    )
    metrics.set_meter_provider(meter_provider)

    logger.info("telemetry exporting via OTLP")
    # Shutdown каждого provider выполняет финальный flush.
    return Telemetry([tracer_provider.shutdown, meter_provider.shutdown])
