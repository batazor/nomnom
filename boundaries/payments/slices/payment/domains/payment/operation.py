"""Payment Operation: one logical provider operation with a stable result."""

from dataclasses import dataclass
from datetime import datetime
from enum import StrEnum

from .events import PaymentDomainEvent, PaymentOperationDeclined
from .fsm import PaymentTrigger
from .ids import OperationId, OperationKind, PaymentId
from .payment import Payment
from .vo.money import Money


class OperationState(StrEnum):
    """Lifecycle of a Payment Operation: ``pending`` until one final result."""

    PENDING = "pending"
    SUCCEEDED = "succeeded"
    DECLINED = "declined"


class OperationTrigger(StrEnum):
    """Final Provider result applied to a pending Payment Operation."""

    SUCCEED = "succeed"
    DECLINE = "decline"


@dataclass(eq=True)
class InvalidOperationTransitionError(Exception):
    """The operation already has the opposite final result; results never change."""

    from_state: OperationState
    trigger: OperationTrigger

    def __str__(self) -> str:
        return (
            f"invalid payment operation transition from {self.from_state.value} "
            f"with {self.trigger.value}"
        )


@dataclass(eq=True)
class OperationAlreadyCompletedError(Exception):
    """The operation already has this final result, so the call is a repetition."""

    state: OperationState

    def __str__(self) -> str:
        return f"payment operation is already {self.state.value}"


@dataclass(eq=True)
class OperationMismatchError(Exception):
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
        raise OperationAlreadyCompletedError(state)
    raise InvalidOperationTransitionError(state, trigger)


class PaymentOperation:
    """One logical Authorization, Capture, Cancel Authorization or Refund of a Payment.

    It is identified by its idempotency key, starts ``pending`` and receives exactly
    one final result. A declined operation records ``PaymentOperationDeclined``.
    Raises ``ValueError`` when created with a blank idempotency key.
    """

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
        """Rebuild an operation from storage without recording events.

        Raises:
            ValueError: The stored state is inconsistent: a completed operation without
                completion time or a declined one without a reason.
        """
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
        """Record a successful final result.

        Raises:
            OperationAlreadyCompletedError: The operation already succeeded.
            InvalidOperationTransitionError: The operation was already declined.
        """
        self._state = _transition(self._state, OperationTrigger.SUCCEED)
        self._provider_reference = provider_reference.strip() if provider_reference else None
        self._completed_at = completed_at

    def decline(self, reason: str, completed_at: datetime) -> None:
        """Record a decline and the ``PaymentOperationDeclined`` event.

        Raises:
            ValueError: ``reason`` is blank.
            OperationAlreadyCompletedError: The operation was already declined.
            InvalidOperationTransitionError: The operation already succeeded.
        """
        if not reason.strip():
            raise ValueError("decline reason must not be empty")
        self._state = _transition(self._state, OperationTrigger.DECLINE)
        self._decline_reason = reason.strip()
        self._completed_at = completed_at
        self._events.append(
            PaymentOperationDeclined(self._id, self._payment_id, self._kind, self._decline_reason)
        )

    def is_for(self, payment_id: PaymentId, kind: OperationKind) -> bool:
        """Whether the operation is of ``kind`` and belongs to the Payment."""
        return self._payment_id == payment_id and self._kind is kind

    def pull_events(self) -> list[PaymentOperationDeclined]:
        """Return the recorded events and forget them, so each is published once."""
        events = list(self._events)
        self._events.clear()
        return events

    @property
    def id(self) -> OperationId:
        """Identifier, also used as the merchant reference at the Provider."""
        return self._id

    @property
    def payment_id(self) -> PaymentId:
        """Payment the operation belongs to."""
        return self._payment_id

    @property
    def kind(self) -> OperationKind:
        """Which Provider operation is requested."""
        return self._kind

    @property
    def idempotency_key(self) -> str:
        """Caller-supplied key that makes repeated requests return this operation."""
        return self._idempotency_key

    @property
    def amount(self) -> Money:
        """Amount the operation is requested for."""
        return self._amount

    @property
    def requested_at(self) -> datetime:
        """When the operation was requested; operations of a Payment are ordered by it."""
        return self._requested_at

    @property
    def state(self) -> OperationState:
        """Current lifecycle state."""
        return self._state

    @property
    def is_completed(self) -> bool:
        """Whether a final result (success or decline) is recorded."""
        return self._state is not OperationState.PENDING

    @property
    def provider_reference(self) -> str | None:
        """Provider Reference of a successful operation, if the Provider returned one."""
        return self._provider_reference

    @property
    def decline_reason(self) -> str | None:
        """Provider's reason of a declined operation; ``None`` otherwise."""
        return self._decline_reason

    @property
    def completed_at(self) -> datetime | None:
        """When the final result was recorded; ``None`` while pending."""
        return self._completed_at

    @property
    def version(self) -> int:
        """Version loaded from storage for optimistic concurrency; 0 for a new operation."""
        return self._version


@dataclass(frozen=True)
class OperationOutcome:
    """Final provider result of a Payment Operation, passed into the domain as a value."""

    succeeded: bool
    provider_reference: str | None = None
    decline_reason: str | None = None

    @classmethod
    def success(cls, provider_reference: str | None = None) -> OperationOutcome:
        """Build a successful outcome."""
        return cls(True, provider_reference=provider_reference)

    @classmethod
    def declined(cls, reason: str) -> OperationOutcome:
        """Build a declined outcome with the Provider's reason."""
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
        raise OperationMismatchError(operation.id, payment.id)
    if operation.is_completed:
        raise OperationAlreadyCompletedError(operation.state)
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
