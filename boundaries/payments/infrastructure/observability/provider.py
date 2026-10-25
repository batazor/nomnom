"""Telemetry of the payment provider client (``provider`` inventory section).

``ObservedProvider`` wraps the ``PaymentProviderGateway`` port: one client span
``payments.provider <operation>`` and one ``nomnom.payments.provider.request.duration``
point per request. Attributes are fixed by ``ops/semconv/model/provider.yaml``:
``nomnom.payment.operation`` (authorize | capture | refund | cancel) and
``nomnom.outcome`` (success | declined | error | timeout). ``declined`` is a business
result; amounts, references and decline reasons never become attributes.
"""

import time
from enum import StrEnum

from opentelemetry import metrics, trace
from opentelemetry.trace import SpanKind, Status, StatusCode

from pkg.telemetry import SECONDS_BUCKETS
from slices.payment.applications.ports import (
    PaymentProviderGateway,
    ProviderOperationOutcome,
    ProviderOperationRequest,
    ProviderOperationStatus,
)
from slices.payment.domains.payment import OperationKind

INSTRUMENTATION = "nomnom.payments.provider"

_OPERATION = "nomnom.payment.operation"
_OUTCOME = "nomnom.outcome"
_ERROR_TYPE = "error.type"

_OPERATIONS: dict[OperationKind, str] = {
    OperationKind.AUTHORIZE: "authorize",
    OperationKind.CAPTURE: "capture",
    OperationKind.REFUND: "refund",
    OperationKind.CANCEL_AUTHORIZATION: "cancel",
}


class ProviderOutcome(StrEnum):
    """Closed ``nomnom.outcome`` set of provider requests."""

    SUCCESS = "success"
    DECLINED = "declined"
    ERROR = "error"
    TIMEOUT = "timeout"


def _outcome(result: ProviderOperationOutcome) -> ProviderOutcome:
    # Accepted: провайдер принял запрос, финальный результат придёт webhook'ом.
    if result.status is ProviderOperationStatus.DECLINED:
        return ProviderOutcome.DECLINED
    return ProviderOutcome.SUCCESS


def _failure(error: Exception) -> ProviderOutcome:
    if isinstance(error, TimeoutError) or isinstance(error.__cause__, TimeoutError):
        return ProviderOutcome.TIMEOUT
    return ProviderOutcome.ERROR


class ObservedProvider:
    """``PaymentProviderGateway`` decorator recording the request span and duration."""

    def __init__(
        self,
        inner: PaymentProviderGateway,
        tracer_provider: trace.TracerProvider | None = None,
        meter_provider: metrics.MeterProvider | None = None,
    ) -> None:
        self._inner = inner
        self._tracer = trace.get_tracer(INSTRUMENTATION, tracer_provider=tracer_provider)
        self._duration = metrics.get_meter(
            INSTRUMENTATION, meter_provider=meter_provider
        ).create_histogram(
            "nomnom.payments.provider.request.duration",
            unit="s",
            description="Duration of one payment provider request.",
            explicit_bucket_boundaries_advisory=list(SECONDS_BUCKETS),
        )

    @property
    def inner(self) -> PaymentProviderGateway:
        """The decorated provider gateway."""
        return self._inner

    def execute(self, request: ProviderOperationRequest) -> ProviderOperationOutcome:
        """Forward the request; the result and exceptions pass through unchanged."""
        operation = _OPERATIONS.get(request.kind, "other")
        with self._tracer.start_as_current_span(
            f"payments.provider {operation}",
            kind=SpanKind.CLIENT,
            attributes={_OPERATION: operation},
            record_exception=False,
            set_status_on_exception=False,
        ) as span:
            started = time.perf_counter()
            outcome = ProviderOutcome.ERROR
            try:
                result = self._inner.execute(request)
                outcome = _outcome(result)
                return result
            except Exception as error:
                outcome = _failure(error)
                span.set_attribute(_ERROR_TYPE, type(error).__name__)
                span.set_status(Status(StatusCode.ERROR, outcome.value))
                raise
            finally:
                span.set_attribute(_OUTCOME, outcome.value)
                self._duration.record(
                    time.perf_counter() - started,
                    {_OPERATION: operation, _OUTCOME: outcome.value},
                )
