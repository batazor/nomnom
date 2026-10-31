"""Create Payment: idempotently create the local payment intent of an Order."""

from dataclasses import dataclass

from pkg.authz import Subject
from slices.payment.domains.payment import (
    DuplicatePaymentError,
    InvalidMarketIdError,
    InvalidMoneyError,
    MarketId,
    Money,
    OrderId,
    Payment,
    PaymentId,
    PaymentRepository,
    PaymentState,
)

from ..access import require_permission
from ..errors import InvalidPaymentRequestError, PaymentAlreadyExistsError
from ..ports import PaymentIdSource, PaymentPermission, PaymentPermissionChecker, PaymentResource


@dataclass(frozen=True)
class CreatePaymentInput:
    """Request to create the Payment of an Order in its Market.

    ``amount`` is an exact decimal string; ``market_id`` is the Market of the Order.
    """

    subject: Subject
    order_id: str
    market_id: str
    amount: str
    currency: str


@dataclass(frozen=True)
class CreatePaymentResult:
    """Created or already existing Payment; ``created`` is ``False`` on a repetition."""

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
        ids: PaymentIdSource,
    ) -> None:
        self._payments = payments
        self._permissions = permissions
        self._ids = ids

    def handle(self, command: CreatePaymentInput) -> CreatePaymentResult:
        """Create the Payment of the Order, or return the existing one with the same terms.

        Raises:
            InvalidPaymentRequestError: The order id is blank, the market id is not a
                Market slug or the amount is not a positive exact decimal in an ISO 4217
                currency.
            PermissionDeniedError: The subject may not create the Payment.
            PaymentAlreadyExistsError: The Order already has a Payment with another
                Market or amount.
        """
        if not command.order_id.strip():
            raise InvalidPaymentRequestError("order id is required")
        order_id = OrderId(command.order_id)
        market_id = _market(command.market_id)
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
            return _same_terms(existing, market_id, amount)
        payment = Payment(payment_id, order_id, market_id, amount)
        try:
            self._payments.add(payment)
        except DuplicatePaymentError:
            concurrent = self._payments.get_by_order(order_id)
            if concurrent is None:
                raise
            return _same_terms(concurrent, market_id, amount)
        return CreatePaymentResult(payment.id, payment.state, created=True)


def _market(market_id: str) -> MarketId:
    try:
        return MarketId(market_id)
    except InvalidMarketIdError as error:
        raise InvalidPaymentRequestError(str(error)) from error


def _positive_money(amount: str, currency: str) -> Money:
    try:
        money = Money.parse(amount, currency)
    except InvalidMoneyError as error:
        raise InvalidPaymentRequestError(str(error)) from error
    if money.amount <= 0:
        raise InvalidPaymentRequestError("payment amount must be positive")
    return money


def _same_terms(existing: Payment, market_id: MarketId, amount: Money) -> CreatePaymentResult:
    if existing.market_id != market_id or existing.amount != amount:
        raise PaymentAlreadyExistsError(existing.order_id)
    return CreatePaymentResult(existing.id, existing.state, created=False)
