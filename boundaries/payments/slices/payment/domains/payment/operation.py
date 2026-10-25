"""Payment Operation: one logical provider operation with a stable result."""

from dataclasses import dataclass
from datetime import datetime
from enum import Enum

from .events import PaymentDomainEvent, PaymentOperationDeclined
from .fsm import PaymentTrigger
from .ids import OperationId, OperationKind, PaymentId
from .payment import Payment
from .vo.money import Money


class OperationState(str, Enum):
    PENDING = "pending"
    SUCCEEDED = "succeeded"
    DECLINED = "declined"


class OperationTrigger(str, Enum):
    SUCCEED = "succeed"
    DECLINE = "decline"


@dataclass(eq=True)
class InvalidOperationTransition(Exception):
    from_state: OperationState
    trigger: OperationTrigger

    def __str__(self) -> str:
        return (
            f"invalid payment operation transition from {self.from_state.value} "
            f"with {self.trigger.value}"
        )


@dataclass(eq=True)
class OperationAlreadyCompleted(Exception):
    state: OperationState

    def __str__(self) -> str:
        return f"payment operation is already {self.state.value}"


@dataclass(eq=True)
class OperationMismatch(Exception):
    """Raised when an operation is completed against a payment it does not belong to."""

    operation_id: OperationId
    payment_id: PaymentId

    def __str__(self) -> str:
        return f"payment operation {self.operation_id} does not belong to {self.payment_id}"


# Single source of truth for every OperationState x OperationTrigger pair:
# a target state, or None when the trigger repeats or contradicts a completed result.
OPERATION_TRANSITIONS: dict[tuple[OperationState, OperationTrigger], OperationState | None] = {
    (OperationState.PENDING, OperationTrigger.SUCCEED): OperationState.SUCCEEDED,
    (OperationState.PENDING, OperationTrigger.DECLINE): OperationState.DECLINED,
    (OperationState.SUCCEEDED, OperationTrigger.SUCCEED): None,
    (OperationState.SUCCEEDED, OperationTrigger.DECLINE): None,
    (OperationState.DECLINED, OperationTrigger.SUCCEED): None,
    (OperationState.DECLINED, OperationTrigger.DECLINE): None,
}

_TARGET_OF = {
    OperationTrigger.SUCCEED: OperationState.SUCCEEDED,
    OperationTrigger.DECLINE: OperationState.DECLINED,
}

PAYMENT_TRIGGER_OF: dict[OperationKind, PaymentTrigger] = {
    OperationKind.AUTHORIZE: PaymentTrigger.AUTHORIZE,
    OperationKind.CAPTURE: PaymentTrigger.CAPTURE,
    OperationKind.CANCEL_AUTHORIZATION: PaymentTrigger.CANCEL_AUTHORIZATION,
    OperationKind.REFUND: PaymentTrigger.REFUND,
}


def _transition(state: OperationState, trigger: OperationTrigger) -> OperationState:
    target = OPERATION_TRANSITIONS[(state, trigger)]
    if target is not None:
        return target
    if _TARGET_OF[trigger] is state:
        raise OperationAlreadyCompleted(state)
    raise InvalidOperationTransition(state, trigger)


class PaymentOperation:
    def __init__(
        self,
        operation_id: OperationId,
        payment_id: PaymentId,
        kind: OperationKind,
        idempotency_key: str,
        amount: Money,
        requested_at: datetime,
    ) -> None:
        if not idempotency_key.strip():
            raise ValueError("payment operation requires an idempotency key")
        self._id = operation_id
        self._payment_id = payment_id
        self._kind = kind
        self._idempotency_key = idempotency_key
        self._amount = amount
        self._requested_at = requested_at
        self._state = OperationState.PENDING
        self._provider_reference: str | None = None
        self._decline_reason: str | None = None
        self._completed_at: datetime | None = None
        self._version = 0
        self._events: list[PaymentOperationDeclined] = []

    @classmethod
    def restore(
        cls,
        operation_id: OperationId,
        payment_id: PaymentId,
        kind: OperationKind,
        idempotency_key: str,
        amount: Money,
        requested_at: datetime,
        state: OperationState,
        provider_reference: str | None,
        decline_reason: str | None,
        completed_at: datetime | None,
        version: int,
    ) -> PaymentOperation:
        if state is not OperationState.PENDING and completed_at is None:
            raise ValueError("completed payment operation requires completion time")
        if state is OperationState.DECLINED and not decline_reason:
            raise ValueError("declined payment operation requires a reason")
        operation = cls(operation_id, payment_id, kind, idempotency_key, amount, requested_at)
        operation._state = state
        operation._provider_reference = provider_reference
        operation._decline_reason = decline_reason
        operation._completed_at = completed_at
        operation._version = version
        return operation

    def succeed(self, provider_reference: str | None, completed_at: datetime) -> None:
        self._state = _transition(self._state, OperationTrigger.SUCCEED)
        self._provider_reference = provider_reference.strip() if provider_reference else None
        self._completed_at = completed_at

    def decline(self, reason: str, completed_at: datetime) -> None:
        if not reason.strip():
            raise ValueError("decline reason must not be empty")
        self._state = _transition(self._state, OperationTrigger.DECLINE)
        self._decline_reason = reason.strip()
        self._completed_at = completed_at
        self._events.append(
            PaymentOperationDeclined(self._id, self._payment_id, self._kind, self._decline_reason)
        )

    def is_for(self, payment_id: PaymentId, kind: OperationKind) -> bool:
        return self._payment_id == payment_id and self._kind is kind

    def pull_events(self) -> list[PaymentOperationDeclined]:
        events = list(self._events)
        self._events.clear()
        return events

    @property
    def id(self) -> OperationId:
        return self._id

    @property
    def payment_id(self) -> PaymentId:
        return self._payment_id

    @property
    def kind(self) -> OperationKind:
        return self._kind

    @property
    def idempotency_key(self) -> str:
        return self._idempotency_key

    @property
    def amount(self) -> Money:
        return self._amount

    @property
    def requested_at(self) -> datetime:
        return self._requested_at

    @property
    def state(self) -> OperationState:
        return self._state

    @property
    def is_completed(self) -> bool:
        return self._state is not OperationState.PENDING

    @property
    def provider_reference(self) -> str | None:
        return self._provider_reference

    @property
    def decline_reason(self) -> str | None:
        return self._decline_reason

    @property
    def completed_at(self) -> datetime | None:
        return self._completed_at

    @property
    def version(self) -> int:
        return self._version


@dataclass(frozen=True)
class OperationOutcome:
    """Final provider result of a Payment Operation, passed into the domain as a value."""

    succeeded: bool
    provider_reference: str | None = None
    decline_reason: str | None = None

    @classmethod
    def success(cls, provider_reference: str | None = None) -> OperationOutcome:
        return cls(True, provider_reference=provider_reference)

    @classmethod
    def declined(cls, reason: str) -> OperationOutcome:
        return cls(False, decline_reason=reason)


def complete_operation(
    payment: Payment,
    operation: PaymentOperation,
    outcome: OperationOutcome,
    completed_at: datetime,
) -> list[PaymentDomainEvent]:
    """Apply a final provider result to the operation and the payment it belongs to.

    A successful operation performs the matching Payment transition. A declined
    Authorization fails the Payment, because several payment attempts per Order are
    not modelled; other declines keep the Payment state so that the caller can decide
    what to do next (for example release the Authorization after a declined Capture).
    Returns the domain events recorded by both objects, operation events first.
    """
    if operation.payment_id != payment.id:
        raise OperationMismatch(operation.id, payment.id)
    if operation.is_completed:
        raise OperationAlreadyCompleted(operation.state)
    if outcome.succeeded:
        _apply_success(payment, operation.kind, outcome.provider_reference)
        operation.succeed(outcome.provider_reference, completed_at)
    else:
        reason = (outcome.decline_reason or "").strip()
        if not reason:
            raise ValueError("decline reason must not be empty")
        payment.ensure_can(PAYMENT_TRIGGER_OF[operation.kind])
        if operation.kind is OperationKind.AUTHORIZE:
            payment.fail(reason)
        operation.decline(reason, completed_at)
    events: list[PaymentDomainEvent] = []
    events.extend(operation.pull_events())
    events.extend(payment.pull_events())
    return events


def _apply_success(payment: Payment, kind: OperationKind, provider_reference: str | None) -> None:
    if kind is OperationKind.AUTHORIZE:
        payment.authorize(provider_reference or "")
    elif kind is OperationKind.CAPTURE:
        payment.capture()
    elif kind is OperationKind.CANCEL_AUTHORIZATION:
        payment.cancel_authorization()
    else:
        payment.refund()
