"""Business counters of Payment Operations (``domain`` section of the ops/grafana inventory).

A counter changes only at an idempotently confirmed transition: it counts the Domain
Events written to the Outbox inside a Unit of Work and records them after the outer Unit
of Work commits (``committed.CommitScope``). A rollback records nothing, and a replay or
an already applied Provider result writes no events, so it is not counted twice. The
domain layer knows nothing about metrics: ``ObservedOutbox`` wraps the ``PaymentOutbox``
port and ``committed.ObservedUnitOfWork`` the ``UnitOfWork`` port in the composition
root.

Names, units and attributes are fixed by ``ops/semconv/model/domain.yaml``.
"""

from collections.abc import Callable, Sequence
from contextlib import AbstractContextManager
from enum import StrEnum

from opentelemetry import metrics

from pkg.outbox import AggregateRef
from slices.payment.applications.ports import PaymentOutbox
from slices.payment.domains.payment import (
    OPERATION_KINDS,
    PaymentAuthorized,
    PaymentCaptured,
    PaymentDomainEvent,
    PaymentEvent,
    PaymentOperationDeclined,
    PaymentRefunded,
)

from .committed import CommitScope

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
    _Counter.CAPTURES: "Payment captures: captured, declined, failed.",
    _Counter.REFUNDS: "Payment refunds: refunded, declined, failed.",
}

# Business counter of each successful Payment transition, by the Domain Event it records.
# Cancel Authorization has no business counter.
_SUCCESSES: dict[type[PaymentEvent], tuple[_Counter, Outcome]] = {
    PaymentAuthorized: (_Counter.AUTHORIZATIONS, Outcome.AUTHORIZED),
    PaymentCaptured: (_Counter.CAPTURES, Outcome.CAPTURED),
    PaymentRefunded: (_Counter.REFUNDS, Outcome.REFUNDED),
}


def classify(event: PaymentDomainEvent) -> tuple[_Counter, Outcome] | None:
    """Business counter and outcome of a committed Domain Event; ``None`` when not counted.

    A Provider decline of a Payment Operation is a business result (``declined``), not a
    technical failure (``failed``): it is counted on the counter of the kind's success
    event, taken from the domain rule table ``OPERATION_KINDS``. A kind without a rule
    is not counted: metrics never fail a committed Unit of Work.

    ``PaymentFailed`` is not counted: it follows a declined Authorization, which is
    already counted as ``declined``. A technical failure (Provider unavailable) leaves
    the operation pending and writes no event, so ``failed`` of Authorizations has no
    source event yet.
    """
    match event:
        case PaymentOperationDeclined(kind=kind):
            rules = OPERATION_KINDS.get(kind)
            counted = None if rules is None else _SUCCESSES.get(rules.success_event)
            return None if counted is None else (counted[0], Outcome.DECLINED)
        case _:
            return _SUCCESSES.get(type(event))


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
        self._committed = CommitScope[PaymentDomainEvent](
            "payments_domain_metrics_collector", self.record
        )

    def track(self) -> AbstractContextManager[Callable[[], None]]:
        """Collect the events of one Unit of Work and record them if the block succeeds.

        A nested block joins the outer collection, so events are recorded once, after
        the outer Unit of Work commits. An exception discards them.
        """
        return self._committed.track()

    def appended(self, events: Sequence[PaymentDomainEvent]) -> None:
        """Note events written to the Outbox; outside a Unit of Work they are already durable."""
        self._committed.add(events)

    def record(self, events: Sequence[PaymentDomainEvent]) -> None:
        """Add the committed events to their counters; other events are ignored."""
        for event in events:
            classified = classify(event)
            if classified is None:
                continue
            counter, outcome = classified
            self._counters[counter].add(1, {_OUTCOME: outcome.value})


class ObservedOutbox:
    """``PaymentOutbox`` that hands successfully written events to ``DomainMetrics``."""

    def __init__(self, inner: PaymentOutbox, metrics: DomainMetrics) -> None:
        self._inner = inner
        self._metrics = metrics

    def add(self, aggregate: AggregateRef, events: Sequence[PaymentDomainEvent]) -> None:
        """Write the events, then note them for the active Unit of Work."""
        self._inner.add(aggregate, events)
        self._metrics.appended(events)
