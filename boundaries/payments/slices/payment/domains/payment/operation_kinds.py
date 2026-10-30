"""Rules of every Operation Kind: the single place that says what each kind does.

Each financial Payment Operation kind has one explicit rule: the permission it requires,
the Payment trigger it performs, the Payment fact on success and the Payment facts on
decline. A kind without a rule is an error, never the action of another kind.
"""

from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from typing import override

from .events import (
    PaymentAuthorizationCancelled,
    PaymentAuthorized,
    PaymentCaptured,
    PaymentEvent,
    PaymentFailed,
    PaymentRefunded,
)
from .fsm import PaymentTrigger, TransitionInput, transition
from .ids import OperationKind
from .model import PaymentModel
from .permissions import PaymentPermission


@dataclass(eq=True)
class UnknownOperationKindError(Exception):
    """No rule is defined for the operation kind, so its result cannot be applied."""

    kind: OperationKind

    @override
    def __str__(self) -> str:
        return f"no rule for payment operation kind {self.kind}"


def _authorize(payment: PaymentModel, provider_reference: str | None) -> PaymentEvent:
    reference = provider_reference or ""
    transition(
        payment.state,
        PaymentTrigger.AUTHORIZE,
        TransitionInput(provider_reference=reference),
    )
    return PaymentAuthorized(
        payment.id, payment.order_id, payment.market_id, payment.amount, reference.strip()
    )


def _capture(payment: PaymentModel, _provider_reference: str | None) -> PaymentEvent:
    transition(payment.state, PaymentTrigger.CAPTURE)
    return PaymentCaptured(payment.id, payment.order_id, payment.market_id, payment.amount)


def _cancel_authorization(payment: PaymentModel, _provider_reference: str | None) -> PaymentEvent:
    transition(payment.state, PaymentTrigger.CANCEL_AUTHORIZATION)
    return PaymentAuthorizationCancelled(
        payment.id, payment.order_id, payment.market_id, payment.amount
    )


def _refund(payment: PaymentModel, _provider_reference: str | None) -> PaymentEvent:
    transition(payment.state, PaymentTrigger.REFUND)
    return PaymentRefunded(payment.id, payment.order_id, payment.market_id, payment.amount)


def _fail_payment(payment: PaymentModel, reason: str) -> Sequence[PaymentEvent]:
    # Several payment attempts per Order are not modelled: a declined Authorization
    # ends the Payment.
    transition(payment.state, PaymentTrigger.FAIL)
    return (PaymentFailed(payment.id, payment.order_id, payment.market_id, reason),)


def _keep_payment(_payment: PaymentModel, _reason: str) -> Sequence[PaymentEvent]:
    # The caller decides what to do next, for example release the Authorization
    # after a declined Capture.
    return ()


@dataclass(frozen=True)
class OperationKindRules:
    """What an Operation Kind requires and does to its Payment.

    ``on_success`` decides the Payment fact of a successful operation, ``success_event``;
    ``on_decline`` decides the Payment facts of a decline. Both raise the state machine
    errors and change nothing.
    """

    permission: PaymentPermission
    trigger: PaymentTrigger
    on_success: Callable[[PaymentModel, str | None], PaymentEvent]
    success_event: type[PaymentEvent]
    on_decline: Callable[[PaymentModel, str], Sequence[PaymentEvent]]


OPERATION_KINDS: Mapping[OperationKind, OperationKindRules] = {
    OperationKind.AUTHORIZE: OperationKindRules(
        permission=PaymentPermission.AUTHORIZE,
        trigger=PaymentTrigger.AUTHORIZE,
        on_success=_authorize,
        success_event=PaymentAuthorized,
        on_decline=_fail_payment,
    ),
    OperationKind.CAPTURE: OperationKindRules(
        permission=PaymentPermission.CAPTURE,
        trigger=PaymentTrigger.CAPTURE,
        on_success=_capture,
        success_event=PaymentCaptured,
        on_decline=_keep_payment,
    ),
    OperationKind.CANCEL_AUTHORIZATION: OperationKindRules(
        permission=PaymentPermission.CANCEL_AUTHORIZATION,
        trigger=PaymentTrigger.CANCEL_AUTHORIZATION,
        on_success=_cancel_authorization,
        success_event=PaymentAuthorizationCancelled,
        on_decline=_keep_payment,
    ),
    OperationKind.REFUND: OperationKindRules(
        permission=PaymentPermission.REFUND,
        trigger=PaymentTrigger.REFUND,
        on_success=_refund,
        success_event=PaymentRefunded,
        on_decline=_keep_payment,
    ),
}


def rules_of(kind: OperationKind) -> OperationKindRules:
    """Return the rule of ``kind``.

    Raises:
        UnknownOperationKindError: No rule is defined for ``kind``.
    """
    rules = OPERATION_KINDS.get(kind)
    if rules is None:
        raise UnknownOperationKindError(kind)
    return rules
