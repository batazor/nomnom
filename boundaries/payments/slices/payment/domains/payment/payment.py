"""Payment aggregate driven by the Payment state machine."""

from .events import (
    PaymentAuthorizationCancelled,
    PaymentAuthorized,
    PaymentCaptured,
    PaymentEvent,
    PaymentFailed,
    PaymentRefunded,
)
from .fsm import (
    PaymentState,
    PaymentTrigger,
    TransitionInput,
    TransitionResult,
    ensure_allowed,
    transition,
)
from .ids import OrderId, PaymentId
from .vo.money import Money

# Leaf states reached only through a successful Authorization. A data invariant, not a
# compound state: it cuts across ``uncaptured`` and ``closed``.
_STATES_WITH_PROVIDER_REFERENCE = frozenset(
    {PaymentState.AUTHORIZED, PaymentState.CAPTURED, PaymentState.REFUNDED}
)


class Payment:
    """Aggregate of a monetary operation for one Order.

    State changes go only through the Payment state machine; each successful change
    records a domain event to be pulled with ``pull_events`` and its transition to be
    pulled with ``pull_transitions``. Raises ``ValueError`` when created with a
    non-positive amount.
    """

    def __init__(self, payment_id: PaymentId, order_id: OrderId, amount: Money) -> None:
        if amount.amount <= 0:
            raise ValueError("payment amount must be positive")
        self._id = payment_id
        self._order_id = order_id
        self._amount = amount
        self._state = PaymentState.PENDING
        self._provider_reference: str | None = None
        self._version = 0
        self._events: list[PaymentEvent] = []
        self._transitions: list[TransitionResult] = []

    @classmethod
    def restore(
        cls,
        payment_id: PaymentId,
        order_id: OrderId,
        amount: Money,
        state: PaymentState,
        provider_reference: str | None,
        version: int,
    ) -> Payment:
        """Rebuild a Payment from storage without recording events.

        Raises:
            ValueError: An authorized, captured or refunded state has no Provider Reference.
        """
        if state in _STATES_WITH_PROVIDER_REFERENCE and (
            provider_reference is None or not provider_reference.strip()
        ):
            raise ValueError("successful payment state requires a provider reference")
        payment = cls(payment_id, order_id, amount)
        payment._state = state
        payment._provider_reference = provider_reference
        payment._version = version
        return payment

    def ensure_can(self, trigger: PaymentTrigger) -> None:
        """Check that the transition table allows ``trigger`` without changing state.

        Transition guards depend on inputs that are not known yet (for example a provider
        reference), so only the table outcome is evaluated.
        """
        ensure_allowed(self._state, trigger)

    def authorize(self, provider_reference: str) -> TransitionResult:
        """Move to ``authorized`` and keep the Provider Reference of the Authorization.

        Raises the state machine errors of ``transition``, including ``GuardFailedError``
        for a blank ``provider_reference``.
        """
        result = transition(
            self._state,
            PaymentTrigger.AUTHORIZE,
            TransitionInput(provider_reference=provider_reference),
        )
        self._provider_reference = provider_reference.strip()
        self._state = result.to_state
        self._transitions.append(result)
        self._events.append(PaymentAuthorized(self._id, self._order_id, self._amount))
        return result

    def capture(self) -> TransitionResult:
        """Move to ``captured`` after a full Capture; raises the state machine errors."""
        result = self._apply(PaymentTrigger.CAPTURE)
        self._events.append(PaymentCaptured(self._id, self._order_id, self._amount))
        return result

    def cancel_authorization(self) -> TransitionResult:
        """Release the Authorization and return to ``pending``, dropping the Provider Reference.

        Raises the state machine errors.
        """
        result = self._apply(PaymentTrigger.CANCEL_AUTHORIZATION)
        self._provider_reference = None
        self._events.append(PaymentAuthorizationCancelled(self._id, self._order_id, self._amount))
        return result

    def refund(self) -> TransitionResult:
        """Move to ``refunded`` after a full Refund; raises the state machine errors."""
        result = self._apply(PaymentTrigger.REFUND)
        self._events.append(PaymentRefunded(self._id, self._order_id, self._amount))
        return result

    def fail(self, reason: str) -> TransitionResult:
        """Move to the terminal ``failed`` state with a reason.

        Raises ``ValueError`` for a blank reason and the state machine errors otherwise.
        """
        if not reason.strip():
            raise ValueError("payment failure reason must not be empty")
        result = self._apply(PaymentTrigger.FAIL)
        self._events.append(PaymentFailed(self._id, self._order_id, reason.strip()))
        return result

    def _apply(self, trigger: PaymentTrigger) -> TransitionResult:
        result = transition(self._state, trigger)
        self._state = result.to_state
        self._transitions.append(result)
        return result

    def pull_events(self) -> list[PaymentEvent]:
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
    def id(self) -> PaymentId:
        """Identifier of the Payment."""
        return self._id

    @property
    def order_id(self) -> OrderId:
        """Order the Payment belongs to."""
        return self._order_id

    @property
    def amount(self) -> Money:
        """Full amount of the Payment; partial Capture and Refund are not modelled."""
        return self._amount

    @property
    def state(self) -> PaymentState:
        """Current business state."""
        return self._state

    @property
    def provider_reference(self) -> str | None:
        """Provider Reference of the active Authorization, if any."""
        return self._provider_reference

    @property
    def version(self) -> int:
        """Version loaded from storage for optimistic concurrency; 0 for a new Payment."""
        return self._version
