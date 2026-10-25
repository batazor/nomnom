"""Rules of every Operation Kind: the single place that says what each kind does.

Each financial Payment Operation kind has one explicit rule: the permission it requires,
the Payment trigger it performs, the Payment action on success and the Payment effect
on decline. A kind without a rule is an error, never the action of another kind.
"""

from collections.abc import Callable, Mapping
from dataclasses import dataclass
from typing import override

from .events import (
    PaymentAuthorizationCancelled,
    PaymentAuthorized,
    PaymentCaptured,
    PaymentEvent,
    PaymentRefunded,
)
from .fsm import PaymentTrigger
from .ids import OperationKind
from .payment import Payment
from .permissions import PaymentPermission


@dataclass(eq=True)
class UnknownOperationKindError(Exception):
    """No rule is defined for the operation kind, so its result cannot be applied."""

    kind: OperationKind

    @override
    def __str__(self) -> str:
        return f"no rule for payment operation kind {self.kind}"


def _authorize(payment: Payment, provider_reference: str | None) -> None:
    payment.authorize(provider_reference or "")


def _capture(payment: Payment, _provider_reference: str | None) -> None:
    payment.capture()


def _cancel_authorization(payment: Payment, _provider_reference: str | None) -> None:
    payment.cancel_authorization()


def _refund(payment: Payment, _provider_reference: str | None) -> None:
    payment.refund()


def _fail_payment(payment: Payment, reason: str) -> None:
    # Several payment attempts per Order are not modelled: a declined Authorization
    # ends the Payment.
    payment.fail(reason)


def _keep_payment(_payment: Payment, _reason: str) -> None:
    # The caller decides what to do next, for example release the Authorization
    # after a declined Capture.
    return None


@dataclass(frozen=True)
class OperationKindRules:
    """What an Operation Kind requires and does to its Payment.

    ``on_success`` performs the Payment transition of a successful operation, which
    records ``success_event``; ``on_decline`` applies the Payment effect of a decline.
    """

    permission: PaymentPermission
    trigger: PaymentTrigger
    on_success: Callable[[Payment, str | None], None]
    success_event: type[PaymentEvent]
    on_decline: Callable[[Payment, str], None]


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
