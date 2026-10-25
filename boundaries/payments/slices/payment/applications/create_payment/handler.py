"""Create Payment: idempotently create the local payment intent of an Order."""

from dataclasses import dataclass

from pkg.application import Subject
from slices.payment.domains.payment import (
    DuplicatePaymentError,
    InvalidMoneyError,
    Money,
    OrderId,
    Payment,
    PaymentId,
    PaymentRepository,
    PaymentState,
)

from ..access import require_permission
from ..errors import InvalidPaymentRequestError, PaymentAlreadyExistsError
from ..ports import PaymentIdentifiers, PaymentPermission, PaymentPermissionChecker, PaymentResource


@dataclass(frozen=True)
class CreatePaymentInput:
    subject: Subject
    order_id: str
    amount: str
    currency: str


@dataclass(frozen=True)
class CreatePaymentResult:
    payment_id: PaymentId
    state: PaymentState
    created: bool


class CreatePaymentHandler:
    """One Payment per Order; ``order_id`` is the natural idempotency key.

    A single insert needs no Unit of Work; the storage enforces ``order_id`` uniqueness.
    """

    def __init__(
        self,
        payments: PaymentRepository,
        permissions: PaymentPermissionChecker,
        ids: PaymentIdentifiers,
    ) -> None:
        self._payments = payments
        self._permissions = permissions
        self._ids = ids

    def handle(self, command: CreatePaymentInput) -> CreatePaymentResult:
        if not command.order_id.strip():
            raise InvalidPaymentRequestError("order id is required")
        order_id = OrderId(command.order_id)
        amount = _positive_money(command.amount, command.currency)
        payment_id = self._ids.new_payment_id()
        require_permission(
            self._permissions,
            command.subject,
            PaymentPermission.CREATE,
            PaymentResource(payment_id, order_id),
        )

        existing = self._payments.get_by_order(order_id)
        if existing is not None:
            return _same_terms(existing, amount)
        payment = Payment(payment_id, order_id, amount)
        try:
            self._payments.add(payment)
        except DuplicatePaymentError:
            concurrent = self._payments.get_by_order(order_id)
            if concurrent is None:
                raise
            return _same_terms(concurrent, amount)
        return CreatePaymentResult(payment.id, payment.state, created=True)


def _positive_money(amount: str, currency: str) -> Money:
    try:
        money = Money.parse(amount, currency)
    except InvalidMoneyError as error:
        raise InvalidPaymentRequestError(str(error)) from error
    if money.amount <= 0:
        raise InvalidPaymentRequestError("payment amount must be positive")
    return money


def _same_terms(existing: Payment, amount: Money) -> CreatePaymentResult:
    if existing.amount != amount:
        raise PaymentAlreadyExistsError(existing.order_id)
    return CreatePaymentResult(existing.id, existing.state, created=False)
