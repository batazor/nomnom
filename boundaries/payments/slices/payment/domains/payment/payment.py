"""Payment aggregate: a thin shell over the Decider that keeps its new events."""

from collections.abc import Sequence
from datetime import datetime

from .decider import CompleteOperation, CreatePayment, RequestOperation, decide, evolve
from .events import (
    PaymentAuthorizationCancelled,
    PaymentAuthorized,
    PaymentCaptured,
    PaymentDomainEvent,
    PaymentFailed,
    PaymentRefunded,
)
from .fsm import PaymentState, PaymentTrigger, TransitionResult
from .ids import OperationId, OperationKind, OperationRequester, OrderId, PaymentId
from .model import PaymentModel
from .operation import OperationOutcome, PaymentOperation
from .vo.market import MarketId
from .vo.money import Money

# Facts that move the Payment state machine, by the trigger that decided them.
_TRIGGER_OF: dict[type, PaymentTrigger] = {
    PaymentAuthorized: PaymentTrigger.AUTHORIZE,
    PaymentCaptured: PaymentTrigger.CAPTURE,
    PaymentAuthorizationCancelled: PaymentTrigger.CANCEL_AUTHORIZATION,
    PaymentRefunded: PaymentTrigger.REFUND,
    PaymentFailed: PaymentTrigger.FAIL,
}


def crosses_snapshot_boundary(old_version: int, new_version: int, every: int) -> bool:
    """Whether a commit from ``old_version`` to ``new_version`` passes a multiple of ``every``.

    A commit of several events can jump over the multiple itself, so its quotients are
    compared rather than the new version alone.
    """
    return new_version // every > old_version // every


def _fold(snapshot: PaymentModel | None, events: Sequence[PaymentDomainEvent]) -> PaymentModel:
    model = snapshot
    for event in events:
        model = evolve(model, event)
    if model is None:
        raise ValueError("payment history is empty")
    return model


class Payment:
    """Aggregate of a monetary operation for one Order in the Market of that Order.

    Its state is the fold of its stream (local ADR 2). Each command goes through ``decide``
    and ``evolve``; the new events wait for the repository in ``uncommitted_events``, for
    publication in ``pull_events`` and their state machine transitions in
    ``pull_transitions``. A Payment restored with ``from_history`` has none of them.
    The Market is set at creation and never changes.
    Raises ``ValueError`` when created with a non-positive amount.
    """

    def __init__(
        self, payment_id: PaymentId, order_id: OrderId, market_id: MarketId, amount: Money
    ) -> None:
        events = decide(None, CreatePayment(payment_id, order_id, market_id, amount))
        self._model = _fold(None, events)
        self._version = 0
        self._uncommitted: list[PaymentDomainEvent] = list(events)
        self._events: list[PaymentDomainEvent] = list(events)
        self._transitions: list[TransitionResult] = []

    @classmethod
    def from_history(
        cls, snapshot: PaymentModel | None, version: int, events: Sequence[PaymentDomainEvent]
    ) -> Payment:
        """Rebuild a Payment from a snapshot of ``version`` and the events after it.

        Without a snapshot ``version`` is 0. Records no new events or transitions.

        Raises:
            ValueError: The history is empty or corrupted.
        """
        payment = cls.__new__(cls)
        payment._model = _fold(snapshot, events)
        payment._version = version + len(events)
        payment._uncommitted = []
        payment._events = []
        payment._transitions = []
        return payment

    def request_operation(
        self,
        operation_id: OperationId,
        kind: OperationKind,
        idempotency_key: str,
        requested_at: datetime,
        requested_by: OperationRequester,
    ) -> PaymentOperation:
        """Record a pending Payment Operation and return it; see ``decide`` for the errors."""
        self._record(
            decide(
                self.model,
                RequestOperation(operation_id, kind, idempotency_key, requested_at, requested_by),
            )
        )
        return self._model.operations[-1]

    def complete_operation(
        self, operation_id: OperationId, outcome: OperationOutcome, completed_at: datetime
    ) -> list[PaymentDomainEvent]:
        """Apply a final provider result and return the new events, operation events first.

        The rule of the operation kind (``OPERATION_KINDS``) decides the Payment effect: a
        successful operation performs the kind's Payment transition; a declined one checks
        that the Payment still allows the kind's trigger and applies the kind's decline
        effect (a declined Authorization fails the Payment, other declines keep its state).
        Raises the errors of ``decide``; nothing changes then.
        """
        self._record(decide(self.model, CompleteOperation(operation_id, outcome, completed_at)))
        return self.pull_events()

    def _record(self, events: Sequence[PaymentDomainEvent]) -> None:
        for event in events:
            before = self._model.state
            self._model = evolve(self._model, event)
            trigger = _TRIGGER_OF.get(type(event))
            if trigger is not None:
                self._transitions.append(TransitionResult(before, trigger, self._model.state))
        self._uncommitted.extend(events)
        self._events.extend(events)

    def pull_events(self) -> list[PaymentDomainEvent]:
        """Return the recorded events and forget them, so each is published once."""
        events = list(self._events)
        self._events.clear()
        return events

    def pull_transitions(self) -> list[TransitionResult]:
        """Return the applied state machine transitions and forget them."""
        transitions = list(self._transitions)
        self._transitions.clear()
        return transitions

    @property
    def uncommitted_events(self) -> tuple[PaymentDomainEvent, ...]:
        """New events the repository has not appended to the stream yet."""
        return tuple(self._uncommitted)

    def mark_committed(self, version: int) -> None:
        """Note that the repository appended the new events and the stream is at ``version``."""
        self._uncommitted.clear()
        self._version = version

    @property
    def model(self) -> PaymentModel:
        """Current immutable state, also the content of a snapshot."""
        return self._model

    @property
    def id(self) -> PaymentId:
        """Identifier of the Payment."""
        return self.model.id

    @property
    def order_id(self) -> OrderId:
        """Order the Payment belongs to."""
        return self.model.order_id

    @property
    def market_id(self) -> MarketId:
        """Market of the Order, fixed at creation."""
        return self.model.market_id

    @property
    def amount(self) -> Money:
        """Full amount of the Payment; partial Capture and Refund are not modelled."""
        return self.model.amount

    @property
    def state(self) -> PaymentState:
        """Current business state."""
        return self.model.state

    @property
    def provider_reference(self) -> str | None:
        """Provider Reference of the active Authorization, if any."""
        return self.model.provider_reference

    @property
    def operations(self) -> tuple[PaymentOperation, ...]:
        """Operations of the Payment in request order."""
        return self.model.operations

    @property
    def version(self) -> int:
        """Version of the stream this state stands at; 0 for a new Payment."""
        return self._version
