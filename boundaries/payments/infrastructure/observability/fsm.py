"""Transitions of the Payment state machine (``fsm`` section of the ops/grafana inventory).

``FsmMetrics`` implements the ``PaymentTransitionObserver`` port: an applied transition
counts after the outer Unit of Work commits (``committed.CommitScope``), a rejected
trigger at once, since its Unit of Work rolls back. Attributes are fixed by
``ops/semconv/model/fsm.yaml`` and take values only from the closed sets of the FSM table:
``nomnom.fsm.aggregate_type=payment``, ``nomnom.fsm.from_state`` / ``to_state``
(``PaymentState``), ``nomnom.fsm.trigger`` (``PaymentTrigger``), ``nomnom.fsm.result``
applied | rejected and ``nomnom.fsm.terminal``. A rejected trigger keeps the state:
``to_state`` equals ``from_state``.
"""

from collections.abc import Callable, Sequence
from contextlib import AbstractContextManager
from enum import StrEnum

from opentelemetry import metrics

from slices.payment.domains.payment import (
    TERMINAL_STATES,
    InvalidTransitionError,
    PaymentState,
    PaymentTrigger,
    TransitionResult,
)

from .committed import CommitScope

INSTRUMENTATION = "nomnom.payments.fsm"

AGGREGATE_TYPE = "payment"

_AGGREGATE_TYPE = "nomnom.fsm.aggregate_type"
_FROM_STATE = "nomnom.fsm.from_state"
_TO_STATE = "nomnom.fsm.to_state"
_TRIGGER = "nomnom.fsm.trigger"
_RESULT = "nomnom.fsm.result"
_TERMINAL = "nomnom.fsm.terminal"


class TransitionOutcome(StrEnum):
    """Closed ``nomnom.fsm.result`` set."""

    APPLIED = "applied"
    # Триггер запрещён таблицей переходов в текущем состоянии.
    REJECTED = "rejected"


class FsmMetrics:
    """``nomnom.fsm.transitions`` of the Payment aggregate.

    Prometheus name: ``nomnom_fsm_transitions_total``. The instrument is created on the
    global MeterProvider by default: no-op until telemetry setup.
    """

    def __init__(self, meter: metrics.Meter | None = None) -> None:
        meter = meter or metrics.get_meter(INSTRUMENTATION)
        self._transitions = meter.create_counter(
            "nomnom.fsm.transitions",
            unit="{transition}",
            description="Applied FSM transitions.",
        )
        self._committed = CommitScope[TransitionResult]("payments_fsm_transitions", self._record)

    def track(self) -> AbstractContextManager[Callable[[], None]]:
        """Scope of one Unit of Work: applied transitions count after its commit."""
        return self._committed.track()

    def applied(self, transitions: Sequence[TransitionResult]) -> None:
        """Note applied transitions; outside a Unit of Work they count at once."""
        self._committed.add(transitions)

    def rejected(self, error: InvalidTransitionError) -> None:
        """Count a trigger the table rejected; the state stays ``from_state``."""
        self._add(error.from_state, error.trigger, error.from_state, TransitionOutcome.REJECTED)

    def _record(self, transitions: Sequence[TransitionResult]) -> None:
        for transition in transitions:
            self._add(
                transition.from_state,
                transition.trigger,
                transition.to_state,
                TransitionOutcome.APPLIED,
            )

    def _add(
        self,
        from_state: PaymentState,
        trigger: PaymentTrigger,
        to_state: PaymentState,
        result: TransitionOutcome,
    ) -> None:
        self._transitions.add(
            1,
            {
                _AGGREGATE_TYPE: AGGREGATE_TYPE,
                _FROM_STATE: from_state.value,
                _TO_STATE: to_state.value,
                _TRIGGER: trigger.value,
                _RESULT: result.value,
                _TERMINAL: to_state in TERMINAL_STATES,
            },
        )
