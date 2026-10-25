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


class Payment:
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
        if state in {
            PaymentState.AUTHORIZED,
            PaymentState.CAPTURED,
            PaymentState.REFUNDED,
        } and (provider_reference is None or not provider_reference.strip()):
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
        result = transition(
            self._state,
            PaymentTrigger.AUTHORIZE,
            TransitionInput(provider_reference=provider_reference),
        )
        self._provider_reference = provider_reference.strip()
        self._state = result.to_state
        self._events.append(PaymentAuthorized(self._id, self._order_id, self._amount))
        return result

    def capture(self) -> TransitionResult:
        result = self._apply(PaymentTrigger.CAPTURE)
        self._events.append(PaymentCaptured(self._id, self._order_id, self._amount))
        return result

    def cancel_authorization(self) -> TransitionResult:
        result = self._apply(PaymentTrigger.CANCEL_AUTHORIZATION)
        self._provider_reference = None
        self._events.append(PaymentAuthorizationCancelled(self._id, self._order_id, self._amount))
        return result

    def refund(self) -> TransitionResult:
        result = self._apply(PaymentTrigger.REFUND)
        self._events.append(PaymentRefunded(self._id, self._order_id, self._amount))
        return result

    def fail(self, reason: str) -> TransitionResult:
        if not reason.strip():
            raise ValueError("payment failure reason must not be empty")
        result = self._apply(PaymentTrigger.FAIL)
        self._events.append(PaymentFailed(self._id, self._order_id, reason.strip()))
        return result

    def _apply(self, trigger: PaymentTrigger) -> TransitionResult:
        result = transition(self._state, trigger)
        self._state = result.to_state
        return result

    def pull_events(self) -> list[PaymentEvent]:
        events = list(self._events)
        self._events.clear()
        return events

    @property
    def id(self) -> PaymentId:
        return self._id

    @property
    def order_id(self) -> OrderId:
        return self._order_id

    @property
    def amount(self) -> Money:
        return self._amount

    @property
    def state(self) -> PaymentState:
        return self._state

    @property
    def provider_reference(self) -> str | None:
        return self._provider_reference

    @property
    def version(self) -> int:
        return self._version
