"""List Payment Operations: read the attempts and results of a Payment."""

from dataclasses import dataclass
from datetime import datetime
from decimal import Decimal

from pkg.application import Subject
from slices.payment.domains.payment import (
    OperationId,
    OperationKind,
    OperationState,
    PaymentId,
    PaymentOperation,
    PaymentOperationRepository,
    PaymentRepository,
)

from ..access import require_permission
from ..errors import InvalidPaymentRequestError, PaymentNotFoundError
from ..ports import PaymentPermission, PaymentPermissionChecker, PaymentResource


@dataclass(frozen=True)
class ListPaymentOperationsInput:
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


@dataclass(frozen=True)
class ListPaymentOperationsResult:
    payment_id: PaymentId
    operations: tuple[PaymentOperationView, ...]


class ListPaymentOperationsHandler:
    def __init__(
        self,
        payments: PaymentRepository,
        operations: PaymentOperationRepository,
        permissions: PaymentPermissionChecker,
    ) -> None:
        self._payments = payments
        self._operations = operations
        self._permissions = permissions

    def handle(self, query: ListPaymentOperationsInput) -> ListPaymentOperationsResult:
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
        operations = self._operations.list_for_payment(payment_id)
        return ListPaymentOperationsResult(payment_id, tuple(_view(op) for op in operations))


def _view(operation: PaymentOperation) -> PaymentOperationView:
    return PaymentOperationView(
        operation_id=operation.id,
        kind=operation.kind,
        state=operation.state,
        amount=operation.amount.amount,
        currency=operation.amount.currency,
        idempotency_key=operation.idempotency_key,
        requested_at=operation.requested_at,
        completed_at=operation.completed_at,
        decline_reason=operation.decline_reason,
    )
