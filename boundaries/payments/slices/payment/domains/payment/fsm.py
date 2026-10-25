"""Pure, data-defined finite state machine of a Payment."""

from dataclasses import dataclass
from enum import Enum
from typing import Callable, NamedTuple, Optional


class PaymentState(str, Enum):
    PENDING = "pending"
    AUTHORIZED = "authorized"
    CAPTURED = "captured"
    REFUNDED = "refunded"
    FAILED = "failed"


class PaymentTrigger(str, Enum):
    AUTHORIZE = "authorize"
    CANCEL_AUTHORIZATION = "cancel_authorization"
    CAPTURE = "capture"
    REFUND = "refund"
    FAIL = "fail"


@dataclass(frozen=True)
class TransitionResult:
    from_state: PaymentState
    trigger: PaymentTrigger
    to_state: PaymentState


@dataclass(eq=True)
class InvalidTransition(Exception):
    from_state: PaymentState
    trigger: PaymentTrigger

    def __str__(self) -> str:
        return f"invalid payment transition from {self.from_state.value} with {self.trigger.value}"


@dataclass(eq=True)
class GuardFailed(Exception):
    reason: str

    def __str__(self) -> str:
        return f"payment transition guard failed: {self.reason}"


@dataclass(eq=True)
class AlreadyInState(Exception):
    state: PaymentState

    def __str__(self) -> str:
        return f"payment is already in state {self.state.value}"


class TransitionInput(NamedTuple):
    provider_reference: Optional[str] = None


class _Outcome(Enum):
    ALLOWED = "allowed"
    INVALID = "invalid"
    ALREADY_APPLIED = "already_applied"


_Guard = Callable[[TransitionInput], None]


@dataclass(frozen=True)
class _TransitionDefinition:
    outcome: _Outcome
    to_state: Optional[PaymentState] = None
    guard: Optional[_Guard] = None


def _provider_reference_is_present(transition_input: TransitionInput) -> None:
    reference = transition_input.provider_reference
    if reference is None or not reference.strip():
        raise GuardFailed("provider_reference_required")


_ALLOWED = _Outcome.ALLOWED
_INVALID = _TransitionDefinition(_Outcome.INVALID)
_ALREADY = _TransitionDefinition(_Outcome.ALREADY_APPLIED)

# Single source of truth for every PaymentState x PaymentTrigger pair.
_TRANSITION_TABLE: dict[tuple[PaymentState, PaymentTrigger], _TransitionDefinition] = {
    (PaymentState.PENDING, PaymentTrigger.AUTHORIZE): _TransitionDefinition(
        _ALLOWED, PaymentState.AUTHORIZED, _provider_reference_is_present
    ),
    (PaymentState.PENDING, PaymentTrigger.CANCEL_AUTHORIZATION): _ALREADY,
    (PaymentState.PENDING, PaymentTrigger.CAPTURE): _INVALID,
    (PaymentState.PENDING, PaymentTrigger.REFUND): _INVALID,
    (PaymentState.PENDING, PaymentTrigger.FAIL): _TransitionDefinition(
        _ALLOWED, PaymentState.FAILED
    ),
    (PaymentState.AUTHORIZED, PaymentTrigger.AUTHORIZE): _ALREADY,
    (PaymentState.AUTHORIZED, PaymentTrigger.CANCEL_AUTHORIZATION): _TransitionDefinition(
        _ALLOWED, PaymentState.PENDING
    ),
    (PaymentState.AUTHORIZED, PaymentTrigger.CAPTURE): _TransitionDefinition(
        _ALLOWED, PaymentState.CAPTURED
    ),
    (PaymentState.AUTHORIZED, PaymentTrigger.REFUND): _INVALID,
    (PaymentState.AUTHORIZED, PaymentTrigger.FAIL): _TransitionDefinition(
        _ALLOWED, PaymentState.FAILED
    ),
    (PaymentState.CAPTURED, PaymentTrigger.AUTHORIZE): _INVALID,
    (PaymentState.CAPTURED, PaymentTrigger.CANCEL_AUTHORIZATION): _INVALID,
    (PaymentState.CAPTURED, PaymentTrigger.CAPTURE): _ALREADY,
    (PaymentState.CAPTURED, PaymentTrigger.REFUND): _TransitionDefinition(
        _ALLOWED, PaymentState.REFUNDED
    ),
    (PaymentState.CAPTURED, PaymentTrigger.FAIL): _INVALID,
    (PaymentState.REFUNDED, PaymentTrigger.AUTHORIZE): _INVALID,
    (PaymentState.REFUNDED, PaymentTrigger.CANCEL_AUTHORIZATION): _INVALID,
    (PaymentState.REFUNDED, PaymentTrigger.CAPTURE): _INVALID,
    (PaymentState.REFUNDED, PaymentTrigger.REFUND): _ALREADY,
    (PaymentState.REFUNDED, PaymentTrigger.FAIL): _INVALID,
    (PaymentState.FAILED, PaymentTrigger.AUTHORIZE): _INVALID,
    (PaymentState.FAILED, PaymentTrigger.CANCEL_AUTHORIZATION): _INVALID,
    (PaymentState.FAILED, PaymentTrigger.CAPTURE): _INVALID,
    (PaymentState.FAILED, PaymentTrigger.REFUND): _INVALID,
    (PaymentState.FAILED, PaymentTrigger.FAIL): _ALREADY,
}


_EMPTY_INPUT = TransitionInput()


def transition(
    from_state: PaymentState,
    trigger: PaymentTrigger,
    transition_input: TransitionInput = _EMPTY_INPUT,
) -> TransitionResult:
    ensure_allowed(from_state, trigger)
    definition = _TRANSITION_TABLE[(from_state, trigger)]
    if definition.guard is not None:
        definition.guard(transition_input)
    if definition.to_state is None:
        raise RuntimeError("allowed payment transition must define its target state")
    return TransitionResult(from_state, trigger, definition.to_state)


def ensure_allowed(from_state: PaymentState, trigger: PaymentTrigger) -> None:
    """Check the table outcome for ``trigger`` without evaluating guards."""
    definition = _TRANSITION_TABLE.get((from_state, trigger), _INVALID)
    if definition.outcome is _Outcome.INVALID:
        raise InvalidTransition(from_state, trigger)
    if definition.outcome is _Outcome.ALREADY_APPLIED:
        raise AlreadyInState(from_state)
