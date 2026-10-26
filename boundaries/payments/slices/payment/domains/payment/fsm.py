"""Pure statechart of a Payment: ``leaf state + trigger + input -> next leaf state | error``.

States form a hierarchy; a transition declared on a compound state applies to every state
inside it, and the innermost declaration wins. The hierarchy, the transitions and the repeats
are data and the single source of truth; see docs/fsm.md#payment and ADR 21.
"""

from collections.abc import Callable, Mapping
from dataclasses import dataclass
from enum import StrEnum
from typing import NamedTuple, override


class PaymentState(StrEnum):
    """Leaf state of a Payment: persisted, published and used in metrics."""

    PENDING = "pending"
    AUTHORIZED = "authorized"
    CAPTURED = "captured"
    REFUNDED = "refunded"
    FAILED = "failed"


class PaymentCompoundState(StrEnum):
    """Named group of Payment states from the Ubiquitous Language.

    Never persisted or published: a Payment is always in exactly one ``PaymentState``.
    """

    UNCAPTURED = "uncaptured"
    """No money has been taken yet; the Payment can still fail."""
    CLOSED = "closed"
    """Terminal states."""


type PaymentNode = PaymentState | PaymentCompoundState

# Parent of every node; ``None`` marks a top-level node.
PARENT: Mapping[PaymentNode, PaymentCompoundState | None] = {
    PaymentCompoundState.UNCAPTURED: None,
    PaymentCompoundState.CLOSED: None,
    PaymentState.PENDING: PaymentCompoundState.UNCAPTURED,
    PaymentState.AUTHORIZED: PaymentCompoundState.UNCAPTURED,
    PaymentState.CAPTURED: None,
    PaymentState.REFUNDED: PaymentCompoundState.CLOSED,
    PaymentState.FAILED: PaymentCompoundState.CLOSED,
}


def lineage(node: PaymentNode) -> tuple[PaymentNode, ...]:
    """``node`` followed by its ancestors up to the top level."""
    nodes: list[PaymentNode] = []
    current: PaymentNode | None = node
    while current is not None:
        nodes.append(current)
        current = PARENT[current]
    return tuple(nodes)


def is_in(state: PaymentState, node: PaymentNode) -> bool:
    """Whether ``state`` is ``node`` or lies inside it."""
    return any(ancestor is node for ancestor in lineage(state))


def leaves_of(node: PaymentNode) -> tuple[PaymentState, ...]:
    """Leaf states inside ``node``, in declaration order."""
    return tuple(state for state in PaymentState if is_in(state, node))


TERMINAL_STATES: frozenset[PaymentState] = frozenset(leaves_of(PaymentCompoundState.CLOSED))


class PaymentTrigger(StrEnum):
    """Input of the Payment state machine, one per domain operation."""

    AUTHORIZE = "authorize"
    CANCEL_AUTHORIZATION = "cancel_authorization"
    CAPTURE = "capture"
    REFUND = "refund"
    FAIL = "fail"


@dataclass(frozen=True)
class TransitionResult:
    """A transition the state machine accepted: where it started, why, and where it ends."""

    from_state: PaymentState
    trigger: PaymentTrigger
    to_state: PaymentState


@dataclass(eq=True)
class InvalidTransitionError(Exception):
    """The trigger is not allowed in the current state at all."""

    from_state: PaymentState
    trigger: PaymentTrigger

    @override
    def __str__(self) -> str:
        return f"invalid payment transition from {self.from_state.value} with {self.trigger.value}"


@dataclass(eq=True)
class GuardFailedError(Exception):
    """The transition is declared, but its guard rejected the input."""

    reason: str

    @override
    def __str__(self) -> str:
        return f"payment transition guard failed: {self.reason}"


@dataclass(eq=True)
class AlreadyInStateError(Exception):
    """The trigger repeats an outcome the Payment has already reached.

    Callers can treat it as an idempotent repetition rather than a conflict.
    """

    state: PaymentState

    @override
    def __str__(self) -> str:
        return f"payment is already in state {self.state.value}"


class TransitionInput(NamedTuple):
    """Data that transition guards need; only the Authorization guard uses it today."""

    provider_reference: str | None = None


_Guard = Callable[[TransitionInput], None]


def _provider_reference_is_present(transition_input: TransitionInput) -> None:
    reference = transition_input.provider_reference
    if reference is None or not reference.strip():
        raise GuardFailedError("provider_reference_required")


@dataclass(frozen=True)
class TransitionRow:
    """Transition declared on a leaf or compound ``source``."""

    source: PaymentNode
    trigger: PaymentTrigger
    target: PaymentState
    guard: _Guard | None = None


TRANSITIONS: tuple[TransitionRow, ...] = (
    TransitionRow(
        PaymentState.PENDING,
        PaymentTrigger.AUTHORIZE,
        PaymentState.AUTHORIZED,
        _provider_reference_is_present,
    ),
    TransitionRow(
        PaymentState.AUTHORIZED, PaymentTrigger.CANCEL_AUTHORIZATION, PaymentState.PENDING
    ),
    TransitionRow(PaymentState.AUTHORIZED, PaymentTrigger.CAPTURE, PaymentState.CAPTURED),
    TransitionRow(PaymentState.CAPTURED, PaymentTrigger.REFUND, PaymentState.REFUNDED),
    TransitionRow(PaymentCompoundState.UNCAPTURED, PaymentTrigger.FAIL, PaymentState.FAILED),
)

# Strict repeats of an already reached outcome: explicit ``AlreadyInState``, on leaf states only.
REPEATS: tuple[tuple[PaymentState, PaymentTrigger], ...] = (
    (PaymentState.PENDING, PaymentTrigger.CANCEL_AUTHORIZATION),
    (PaymentState.AUTHORIZED, PaymentTrigger.AUTHORIZE),
    (PaymentState.CAPTURED, PaymentTrigger.CAPTURE),
    (PaymentState.REFUNDED, PaymentTrigger.REFUND),
    (PaymentState.FAILED, PaymentTrigger.FAIL),
)


_EMPTY_INPUT = TransitionInput()


def transition(
    from_state: PaymentState,
    trigger: PaymentTrigger,
    transition_input: TransitionInput = _EMPTY_INPUT,
) -> TransitionResult:
    """Compute the transition for ``trigger`` without mutating anything.

    Raises:
        InvalidTransitionError: Neither ``from_state`` nor its ancestors declare ``trigger``.
        AlreadyInStateError: The trigger repeats an already reached outcome.
        GuardFailedError: The guard rejected ``transition_input``, for example an
            Authorization without a Provider Reference.
    """
    row = _resolve(from_state, trigger)
    if row.guard is not None:
        row.guard(transition_input)
    return TransitionResult(from_state, trigger, row.target)


def ensure_allowed(from_state: PaymentState, trigger: PaymentTrigger) -> None:
    """Check that ``trigger`` resolves to a transition without evaluating guards."""
    _resolve(from_state, trigger)


def _resolve(from_state: PaymentState, trigger: PaymentTrigger) -> TransitionRow:
    """Innermost declaration on ``from_state`` or one of its ancestors wins."""
    for node in lineage(from_state):
        for row in TRANSITIONS:
            if row.source is node and row.trigger is trigger:
                return row
        if any(state is node and repeated is trigger for state, repeated in REPEATS):
            raise AlreadyInStateError(from_state)
    raise InvalidTransitionError(from_state, trigger)
