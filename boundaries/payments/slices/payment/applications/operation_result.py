"""Result of a financial Payment Operation as returned to transports."""

from dataclasses import dataclass
from enum import Enum

from slices.payment.domains.payment import (
    OperationId,
    OperationKind,
    OperationState,
    PaymentId,
    PaymentOperation,
    PaymentState,
)

PAYMENT_AGGREGATE = "Payment"


class OperationStatus(str, Enum):
    SUCCEEDED = "succeeded"
    DECLINED = "declined"
    PENDING = "pending"


_STATUS_OF: dict[OperationState, OperationStatus] = {
    OperationState.SUCCEEDED: OperationStatus.SUCCEEDED,
    OperationState.DECLINED: OperationStatus.DECLINED,
    OperationState.PENDING: OperationStatus.PENDING,
}


@dataclass(frozen=True)
class PaymentOperationResult:
    """Result of a financial operation; provider references are not exposed."""

    payment_id: PaymentId
    operation_id: OperationId
    kind: OperationKind
    status: OperationStatus
    payment_state: PaymentState
    decline_reason: str | None = None
    replayed: bool = False

    @classmethod
    def of(
        cls, operation: PaymentOperation, payment_state: PaymentState, replayed: bool
    ) -> PaymentOperationResult:
        return cls(
            payment_id=operation.payment_id,
            operation_id=operation.id,
            kind=operation.kind,
            status=_STATUS_OF[operation.state],
            payment_state=payment_state,
            decline_reason=operation.decline_reason,
            replayed=replayed,
        )
