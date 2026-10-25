"""Repository contracts of the payment aggregate and its operations.

Implementations live in infrastructure. ``save`` uses optimistic concurrency on the
loaded ``version`` and raises the application concurrency conflict when it changed.
Inside an active Unit of Work every method participates in that transaction.
"""

from dataclasses import dataclass
from typing import Protocol

from .ids import OperationId, OrderId, PaymentId
from .operation import PaymentOperation
from .payment import Payment


@dataclass(eq=True)
class DuplicatePaymentError(Exception):
    """A Payment for the Order already exists (one Payment per Order)."""

    order_id: OrderId

    def __str__(self) -> str:
        return f"payment for order {self.order_id} already exists"


@dataclass(eq=True)
class DuplicateIdempotencyKeyError(Exception):
    """A Payment Operation with the idempotency key already exists."""

    idempotency_key: str

    def __str__(self) -> str:
        return "payment operation with this idempotency key already exists"


@dataclass(eq=True)
class OperationAlreadyPendingError(Exception):
    """The Payment already has an operation without a final result."""

    payment_id: PaymentId

    def __str__(self) -> str:
        return f"payment {self.payment_id} already has a pending operation"


class PaymentRepository(Protocol):
    """Storage of Payment aggregates; at most one Payment per Order."""

    def get(self, payment_id: PaymentId) -> Payment | None:
        """Return the Payment, or ``None`` when it does not exist."""
        ...

    def get_by_order(self, order_id: OrderId) -> Payment | None:
        """Return the only Payment of the Order, or ``None`` when it was not created."""
        ...

    def add(self, payment: Payment) -> int:
        """Persist a new Payment and return its version; raises ``DuplicatePaymentError``."""
        ...

    def save(self, payment: Payment) -> int:
        """Persist changes of a loaded Payment and return the committed version."""
        ...


class PaymentOperationRepository(Protocol):
    """Storage of Payment Operations; idempotency keys are unique across Payments."""

    def get(self, operation_id: OperationId) -> PaymentOperation | None:
        """Return the operation, or ``None`` when it does not exist."""
        ...

    def get_by_idempotency_key(self, idempotency_key: str) -> PaymentOperation | None:
        """Return the operation recorded for the idempotency key, or ``None``."""
        ...

    def find_pending(self, payment_id: PaymentId) -> PaymentOperation | None:
        """Return the operation of the Payment that has no final result yet, if any."""
        ...

    def list_for_payment(self, payment_id: PaymentId) -> list[PaymentOperation]:
        """Return operations of the Payment ordered by ``requested_at``."""
        ...

    def add(self, operation: PaymentOperation) -> None:
        """Persist a new pending operation.

        Raises ``DuplicateIdempotencyKeyError`` or ``OperationAlreadyPendingError``.
        """
        ...

    def save(self, operation: PaymentOperation) -> int:
        """Persist changes of a loaded operation and return the committed version."""
        ...
