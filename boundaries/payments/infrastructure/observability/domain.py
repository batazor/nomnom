"""Business counters of Payment Operations (``domain`` section of the ops/grafana inventory).

A counter changes only at an idempotently confirmed transition: it counts the Domain
Events written to the Outbox inside a Unit of Work and records them after the outer Unit
of Work commits. A rollback records nothing, and a replay or an already applied Provider
result writes no events, so it is not counted twice. The domain layer knows nothing about
metrics: the decorators below wrap the ``UnitOfWork`` and ``PaymentOutbox`` ports in the
composition root.

Names, units and attributes are fixed by ``ops/semconv/model/domain.yaml``.
"""

from collections.abc import Callable, Iterator, Sequence
from contextlib import contextmanager
from contextvars import ContextVar
from enum import StrEnum
from typing import TypeVar

from opentelemetry import metrics

from pkg.application import AggregateRef, UnitOfWork
from slices.payment.applications.ports import PaymentOutbox
from slices.payment.domains.payment import (
    OperationKind,
    PaymentAuthorized,
    PaymentCaptured,
    PaymentDomainEvent,
    PaymentOperationDeclined,
    PaymentRefunded,
)

T = TypeVar("T")

INSTRUMENTATION = "nomnom.payments.domain"
_OUTCOME = "nomnom.outcome"


class Outcome(StrEnum):
    """Closed set of ``nomnom.outcome`` values of the payments business counters."""

    AUTHORIZED = "authorized"
    DECLINED = "declined"
    FAILED = "failed"
    CAPTURED = "captured"
    REFUNDED = "refunded"


class _Counter(StrEnum):
    AUTHORIZATIONS = "nomnom.payments.authorizations"
    CAPTURES = "nomnom.payments.captures"
    REFUNDS = "nomnom.payments.refunds"


_DESCRIPTIONS = {
    _Counter.AUTHORIZATIONS: "Payment authorizations: authorized, declined, failed.",
    _Counter.CAPTURES: "Payment captures: captured, failed.",
    _Counter.REFUNDS: "Payment refunds: refunded, failed.",
}

# Provider decline of a Payment Operation, by kind. An Authorization decline is a
# business result (``declined``); the registry has no ``declined`` for Capture and
# Refund, so their decline is the operation failing (``failed``). Cancel Authorization
# has no business counter.
_DECLINES = {
    OperationKind.AUTHORIZE: (_Counter.AUTHORIZATIONS, Outcome.DECLINED),
    OperationKind.CAPTURE: (_Counter.CAPTURES, Outcome.FAILED),
    OperationKind.REFUND: (_Counter.REFUNDS, Outcome.FAILED),
}


def classify(event: PaymentDomainEvent) -> tuple[_Counter, Outcome] | None:
    """Business counter and outcome of a committed Domain Event; ``None`` when not counted.

    ``PaymentFailed`` is not counted: it follows a declined Authorization, which is
    already counted as ``declined``. A technical failure (Provider unavailable) leaves
    the operation pending and writes no event, so ``failed`` of Authorizations has no
    source event yet.
    """
    if isinstance(event, PaymentAuthorized):
        return _Counter.AUTHORIZATIONS, Outcome.AUTHORIZED
    if isinstance(event, PaymentCaptured):
        return _Counter.CAPTURES, Outcome.CAPTURED
    if isinstance(event, PaymentRefunded):
        return _Counter.REFUNDS, Outcome.REFUNDED
    if isinstance(event, PaymentOperationDeclined):
        return _DECLINES.get(event.kind)
    return None


_collector: ContextVar[list[PaymentDomainEvent] | None] = ContextVar(
    "payments_domain_metrics_collector", default=None
)


class DomainMetrics:
    """Business counters of Payment Operations.

    Prometheus names: ``nomnom_payments_authorizations_total``,
    ``nomnom_payments_captures_total``, ``nomnom_payments_refunds_total``. Instruments
    are created on the global MeterProvider by default: no-op until telemetry setup.
    """

    def __init__(self, meter: metrics.Meter | None = None) -> None:
        meter = meter or metrics.get_meter(INSTRUMENTATION)
        self._counters = {
            counter: meter.create_counter(
                counter.value, unit="{operation}", description=_DESCRIPTIONS[counter]
            )
            for counter in _Counter
        }

    @contextmanager
    def track(self) -> Iterator[None]:
        """Collect the events of one Unit of Work and record them if the block succeeds.

        A nested block joins the outer collection, so events are recorded once, after
        the outer Unit of Work commits. An exception discards them.
        """
        if _collector.get() is not None:
            yield
            return
        events: list[PaymentDomainEvent] = []
        token = _collector.set(events)
        try:
            yield
        finally:
            _collector.reset(token)
        self.record(events)

    def appended(self, events: Sequence[PaymentDomainEvent]) -> None:
        """Note events written to the Outbox; outside a Unit of Work they are already durable."""
        collected = _collector.get()
        if collected is None:
            self.record(events)
        else:
            collected.extend(events)

    def record(self, events: Sequence[PaymentDomainEvent]) -> None:
        """Add the committed events to their counters; other events are ignored."""
        for event in events:
            classified = classify(event)
            if classified is None:
                continue
            counter, outcome = classified
            self._counters[counter].add(1, {_OUTCOME: outcome.value})


class ObservedUnitOfWork:
    """``UnitOfWork`` that records business metrics after the outer transaction commits."""

    def __init__(self, inner: UnitOfWork, metrics: DomainMetrics) -> None:
        self._inner = inner
        self._metrics = metrics

    def run(self, work: Callable[[], T]) -> T:
        """Run ``work`` in the inner Unit of Work; its events count only after a commit."""
        with self._metrics.track():
            return self._inner.run(work)


class ObservedOutbox:
    """``PaymentOutbox`` that hands successfully written events to ``DomainMetrics``."""

    def __init__(self, inner: PaymentOutbox, metrics: DomainMetrics) -> None:
        self._inner = inner
        self._metrics = metrics

    def add(self, aggregate: AggregateRef, events: Sequence[PaymentDomainEvent]) -> None:
        """Write the events, then note them for the active Unit of Work."""
        self._inner.add(aggregate, events)
        self._metrics.appended(events)
