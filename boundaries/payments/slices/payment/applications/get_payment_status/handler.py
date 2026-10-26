"""Get Payment Status: read the permitted view of a Payment without changing data."""

from dataclasses import dataclass
from decimal import Decimal
from typing import Protocol

from pkg.application import Subject
from slices.payment.domains.payment import OrderId, PaymentId, PaymentState

from ..access import require_permission
from ..errors import InvalidPaymentRequestError, PaymentNotFoundError
from ..ports import PaymentPermission, PaymentPermissionChecker, PaymentResource


@dataclass(frozen=True)
class GetPaymentStatusInput:
    """Query of one Payment on behalf of a subject."""

    subject: Subject
    payment_id: str


@dataclass(frozen=True)
class GetPaymentStatusResult:
    """Payment view without Provider Reference or other provider data."""

    payment_id: PaymentId
    order_id: OrderId
    amount: Decimal
    currency: str
    state: PaymentState


class PaymentStatusReader(Protocol):
    """Read model of the query: the result is read from storage, not from the aggregate."""

    def status(self, payment_id: PaymentId) -> GetPaymentStatusResult | None:
        """Return the Payment view, or ``None`` when the Payment does not exist."""
        ...


class GetPaymentStatusHandler:
    """Returns the current state of a Payment the subject may view."""

    def __init__(self, reader: PaymentStatusReader, permissions: PaymentPermissionChecker) -> None:
        self._reader = reader
        self._permissions = permissions

    def handle(self, query: GetPaymentStatusInput) -> GetPaymentStatusResult:
        """Return the Payment view; the permission is checked before the lookup.

        Raises:
            InvalidPaymentRequestError: The payment id is blank.
            PermissionDeniedError: The subject may not view the Payment.
            PaymentNotFoundError: The Payment does not exist.
        """
        if not query.payment_id.strip():
            raise InvalidPaymentRequestError("payment id is required")
        payment_id = PaymentId(query.payment_id)
        require_permission(
            self._permissions, query.subject, PaymentPermission.VIEW, PaymentResource(payment_id)
        )
        view = self._reader.status(payment_id)
        if view is None:
            raise PaymentNotFoundError(payment_id)
        return view
