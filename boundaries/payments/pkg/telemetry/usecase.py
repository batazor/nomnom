"""Use case decorator: one span and one ``nomnom.usecase.duration`` point per call.

Contract of docs/observability.md#use-cases and ``ops/semconv/model/usecase.yaml``:
``nomnom.usecase.name`` (stable snake_case), ``nomnom.usecase.kind`` (command | query),
``nomnom.outcome`` (success | rejected | error) and ``error.type`` from the closed set
of the slice, only when the call failed. The span ``usecase <name>`` carries the same
attributes; its status is ``Error`` only for ``outcome=error``. Error text, IDs,
subjects and payloads never become attributes or span events.

Use cases do not import OpenTelemetry: the decorator wraps the ``Handler`` port in the
composition root, and the slice supplies the classifier of its errors.
"""

import time
from collections.abc import Callable
from dataclasses import dataclass
from enum import StrEnum

from opentelemetry import metrics, trace
from opentelemetry.trace import Status, StatusCode

from pkg.application import Handler

from .buckets import SECONDS_BUCKETS

INSTRUMENTATION = "nomnom.payments.usecase"

USECASE_NAME = "nomnom.usecase.name"
USECASE_KIND = "nomnom.usecase.kind"
OUTCOME = "nomnom.outcome"
ERROR_TYPE = "error.type"

ERROR_TYPE_INTERNAL = "internal"


class Kind(StrEnum):
    """Commands change state, Queries only read it."""

    COMMAND = "command"
    QUERY = "query"


class Outcome(StrEnum):
    """Closed result set of one use case call."""

    SUCCESS = "success"
    # Ожидаемый отказ по правилам use case: невалидный вход, запрет доступа,
    # отсутствующий ресурс, недопустимый переход.
    REJECTED = "rejected"
    # Технический сбой: инфраструктура, конфликт для retry, неклассифицированная ошибка.
    ERROR = "error"


@dataclass(frozen=True)
class Failure:
    """How a failed call is reported; ``error_type`` comes from a closed set."""

    outcome: Outcome
    error_type: str


@dataclass(frozen=True)
class UseCase:
    """Telemetry name of a use case."""

    name: str
    kind: Kind

    @classmethod
    def command(cls, name: str) -> UseCase:
        """A use case that changes state."""
        return cls(name, Kind.COMMAND)

    @classmethod
    def query(cls, name: str) -> UseCase:
        """A use case that only reads state."""
        return cls(name, Kind.QUERY)


# Возвращает None для ошибки, которую slice не знает: она становится error/internal,
# поэтому новый отказ без классификации виден как сбой.
Classifier = Callable[[Exception], Failure | None]


class UseCases:
    """Instruments shared by the decorated handlers of a process.

    Providers default to the global ones: no-op until telemetry setup.
    """

    def __init__(
        self,
        classify: Classifier,
        tracer_provider: trace.TracerProvider | None = None,
        meter_provider: metrics.MeterProvider | None = None,
    ) -> None:
        self._classify = classify
        self._tracer = trace.get_tracer(INSTRUMENTATION, tracer_provider=tracer_provider)
        self._duration = metrics.get_meter(
            INSTRUMENTATION, meter_provider=meter_provider
        ).create_histogram(
            "nomnom.usecase.duration",
            unit="s",
            description="Duration of one application use case call.",
            explicit_bucket_boundaries_advisory=list(SECONDS_BUCKETS),
        )

    def instrument[In, Out](self, use_case: UseCase, handler: Handler[In, Out]) -> Handler[In, Out]:
        """Decorate ``handler``; its result and exceptions pass through unchanged."""
        return InstrumentedHandler(self, use_case, handler)

    def failure(self, error: Exception) -> Failure:
        """Classify a failed call; unknown errors are ``error/internal``."""
        failure = self._classify(error)
        if failure is None or failure.outcome is Outcome.SUCCESS:
            return Failure(Outcome.ERROR, ERROR_TYPE_INTERNAL)
        return failure


class InstrumentedHandler[In, Out]:
    """``Handler`` that records one span and one duration point per call."""

    def __init__(self, use_cases: UseCases, use_case: UseCase, inner: Handler[In, Out]) -> None:
        self._use_cases = use_cases
        self._use_case = use_case
        self._inner = inner

    def handle(self, request: In, /) -> Out:
        """Run the inner handler inside the ``usecase <name>`` span."""
        attributes: dict[str, str] = {
            USECASE_NAME: self._use_case.name,
            USECASE_KIND: self._use_case.kind.value,
        }
        use_cases = self._use_cases
        with use_cases._tracer.start_as_current_span(
            f"usecase {self._use_case.name}",
            attributes=attributes,
            # Текст ошибки может содержать вход: span events не пишутся.
            record_exception=False,
            set_status_on_exception=False,
        ) as span:
            started = time.perf_counter()
            outcome: dict[str, str] = {OUTCOME: Outcome.SUCCESS.value}
            try:
                return self._inner.handle(request)
            except Exception as error:
                failure = use_cases.failure(error)
                outcome = {OUTCOME: failure.outcome.value, ERROR_TYPE: failure.error_type}
                if failure.outcome is Outcome.ERROR:
                    span.set_status(Status(StatusCode.ERROR, failure.error_type))
                raise
            finally:
                span.set_attributes(outcome)
                use_cases._duration.record(time.perf_counter() - started, {**attributes, **outcome})
