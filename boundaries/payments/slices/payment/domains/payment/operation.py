"""Payment Operation: one logical provider operation with a stable result."""

from dataclasses import dataclass
from datetime import datetime
from enum import StrEnum
from typing import override

from .ids import OperationId, OperationKind, OperationRequester, PaymentId
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

    @override
    def __str__(self) -> str:
        return (
            f"invalid payment operation transition from {self.from_state.value} "
            f"with {self.trigger.value}"
        )


@dataclass(eq=True)
class OperationAlreadyCompletedError(Exception):
    """The operation already has this final result, so the call is a repetition."""

    state: OperationState

    @override
    def __str__(self) -> str:
        return f"payment operation is already {self.state.value}"


@dataclass(eq=True)
class OperationMismatchError(Exception):
    """Raised when an operation is completed against a payment it does not belong to."""

    operation_id: OperationId
    payment_id: PaymentId

    @override
    def __str__(self) -> str:
        return f"payment operation {self.operation_id} does not belong to {self.payment_id}"


@dataclass(eq=True)
class OperationAlreadyPendingError(Exception):
    """The Payment already has an operation without a final result."""

    payment_id: PaymentId

    @override
    def __str__(self) -> str:
        return f"payment {self.payment_id} already has a pending operation"


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


def operation_transition(state: OperationState, trigger: OperationTrigger) -> OperationState:
    """Return the state ``trigger`` leads to.

    Raises:
        OperationAlreadyCompletedError: The operation already has this final result.
        InvalidOperationTransitionError: The operation already has the opposite result.
    """
    target = OPERATION_TRANSITIONS[(state, trigger)]
    if target is not None:
        return target
    if _TARGET_OF[trigger] is state:
        raise OperationAlreadyCompletedError(state)
    raise InvalidOperationTransitionError(state, trigger)


@dataclass(frozen=True)
class PaymentOperation:
    """One logical Authorization, Capture, Cancel Authorization or Refund of a Payment.

    An immutable part of the Payment aggregate: it is identified by its idempotency key,
    starts ``pending`` and receives exactly one final result through the events of the
    Payment stream.

    Raises ``ValueError`` when created with a blank idempotency key, or when a completed
    operation has no completion time or a declined one has no reason.
    """

    id: OperationId
    payment_id: PaymentId
    kind: OperationKind
    idempotency_key: str
    amount: Money
    requested_at: datetime
    state: OperationState = OperationState.PENDING
    provider_reference: str | None = None
    decline_reason: str | None = None
    completed_at: datetime | None = None
    requested_by: OperationRequester | None = None

    def __post_init__(self) -> None:
        if not self.idempotency_key.strip():
            raise ValueError("payment operation requires an idempotency key")
        if self.state is not OperationState.PENDING and self.completed_at is None:
            raise ValueError("completed payment operation requires completion time")
        if self.state is OperationState.DECLINED and self.decline_reason in (None, ""):
            raise ValueError("declined payment operation requires a reason")

    def is_for(self, payment_id: PaymentId, kind: OperationKind) -> bool:
        """Whether the operation is of ``kind`` and belongs to the Payment."""
        return self.payment_id == payment_id and self.kind is kind

    @property
    def is_completed(self) -> bool:
        """Whether a final result (success or decline) is recorded."""
        return self.state is not OperationState.PENDING


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
