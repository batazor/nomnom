"""List Payment Operations: read the attempts and results of a Payment."""

from dataclasses import dataclass
from datetime import datetime
from decimal import Decimal
from typing import Protocol

from pkg.application import Subject
from slices.payment.domains.payment import (
    OperationId,
    OperationKind,
    OperationState,
    PaymentId,
    PaymentRepository,
)

from ..access import require_permission
from ..errors import InvalidPaymentRequestError, PaymentNotFoundError
from ..ports import PaymentPermission, PaymentPermissionChecker, PaymentResource


@dataclass(frozen=True)
class ListPaymentOperationsInput:
    """Query of the operations of one Payment on behalf of a subject."""

    subject: Subject
    payment_id: str


@dataclass(frozen=True)
class PaymentOperationView:
    """Operation view without Provider Reference or other provider data."""

    operation_id: OperationId
    kind: OperationKind
    state: OperationState
    amount: Decimal
    currency: str
    idempotency_key: str
    requested_at: datetime
    completed_at: datetime | None
    decline_reason: str | None


class PaymentOperationHistory(Protocol):
    """Read model of the Payment Operations of a Payment."""

    def list_for_payment(self, payment_id: PaymentId) -> list[PaymentOperationView]:
        """Return the operation views of the Payment ordered by ``requested_at``."""
        ...


@dataclass(frozen=True)
class ListPaymentOperationsResult:
    """Operations of the Payment in request order."""

    payment_id: PaymentId
    operations: tuple[PaymentOperationView, ...]


class ListPaymentOperationsHandler:
    """Returns the Payment Operations of a Payment the subject may inspect."""

    def __init__(
        self,
        payments: PaymentRepository,
        history: PaymentOperationHistory,
        permissions: PaymentPermissionChecker,
    ) -> None:
        self._payments = payments
        self._history = history
        self._permissions = permissions

    def handle(self, query: ListPaymentOperationsInput) -> ListPaymentOperationsResult:
        """Return the operations ordered by ``requested_at``.

        Raises:
            InvalidPaymentRequestError: The payment id is blank.
            PermissionDeniedError: The subject may not view the operations.
            PaymentNotFoundError: The Payment does not exist.
        """
        if not query.payment_id.strip():
            raise InvalidPaymentRequestError("payment id is required")
        payment_id = PaymentId(query.payment_id)
        require_permission(
            self._permissions,
            query.subject,
            PaymentPermission.VIEW_OPERATIONS,
            PaymentResource(payment_id),
        )
        if self._payments.get(payment_id) is None:
            raise PaymentNotFoundError(payment_id)
        return ListPaymentOperationsResult(
            payment_id, tuple(self._history.list_for_payment(payment_id))
        )
