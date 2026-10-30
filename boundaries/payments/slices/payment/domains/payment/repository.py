"""Repository contract of the payment aggregate.

Implementations live in infrastructure. ``save`` appends events after the loaded ``version``
and raises the application concurrency conflict when the stream has moved on.
Inside an active Unit of Work every method participates in that transaction.
"""

from dataclasses import dataclass
from typing import Protocol, override

from .ids import OperationId, OrderId, PaymentId
from .payment import Payment


@dataclass(eq=True)
class DuplicatePaymentError(Exception):
    """A Payment for the Order already exists (one Payment per Order)."""

    order_id: OrderId

    @override
    def __str__(self) -> str:
        return f"payment for order {self.order_id} already exists"


@dataclass(eq=True)
class DuplicateIdempotencyKeyError(Exception):
    """A Payment Operation with the idempotency key already exists."""

    idempotency_key: str

    @override
    def __str__(self) -> str:
        return "payment operation with this idempotency key already exists"


class PaymentRepository(Protocol):
    """Storage of Payment aggregates; at most one Payment per Order.

    Idempotency keys are unique across Payments. Lookups by Order, operation and key
    find the Payment through the projections and load it from its stream.
    """

    def get(self, payment_id: PaymentId) -> Payment | None:
        """Return the Payment, or ``None`` when it does not exist."""
        ...

    def get_by_order(self, order_id: OrderId) -> Payment | None:
        """Return the only Payment of the Order, or ``None`` when it was not created."""
        ...

    def get_by_operation(self, operation_id: OperationId) -> Payment | None:
        """Return the Payment holding the operation, or ``None`` for an unknown operation."""
        ...

    def get_by_idempotency_key(self, idempotency_key: str) -> Payment | None:
        """Return the Payment holding the operation with the key, or ``None``."""
        ...

    def add(self, payment: Payment) -> int:
        """Persist a new Payment and return its version; raises ``DuplicatePaymentError``."""
        ...

    def save(self, payment: Payment) -> int:
        """Append the new events of a loaded Payment and return the committed version.

        Raises ``DuplicateIdempotencyKeyError`` when an operation key is already used by
        another Payment.
        """
        ...
