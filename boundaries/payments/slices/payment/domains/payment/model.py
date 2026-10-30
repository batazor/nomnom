"""State of a Payment: what ``decide`` reads and ``evolve`` builds from the stream."""

from dataclasses import dataclass

from .fsm import PaymentState
from .ids import OperationId, OrderId, PaymentId
from .operation import PaymentOperation
from .vo.market import MarketId
from .vo.money import Money

# Leaf states reached only through a successful Authorization. A data invariant, not a
# compound state: it cuts across ``uncaptured`` and ``closed``.
_STATES_WITH_PROVIDER_REFERENCE = frozenset(
    {PaymentState.AUTHORIZED, PaymentState.CAPTURED, PaymentState.REFUNDED}
)


@dataclass(frozen=True)
class PaymentModel:
    """Immutable state of the Payment aggregate with its operations in request order.

    Raises ``ValueError`` when an authorized, captured or refunded state has no Provider
    Reference.
    """

    id: PaymentId
    order_id: OrderId
    market_id: MarketId
    amount: Money
    state: PaymentState = PaymentState.PENDING
    provider_reference: str | None = None
    operations: tuple[PaymentOperation, ...] = ()

    def __post_init__(self) -> None:
        if self.state in _STATES_WITH_PROVIDER_REFERENCE and (
            self.provider_reference is None or not self.provider_reference.strip()
        ):
            raise ValueError("successful payment state requires a provider reference")

    def operation(self, operation_id: OperationId) -> PaymentOperation | None:
        """Return the operation of this Payment with the id, or ``None``."""
        return next((op for op in self.operations if op.id == operation_id), None)

    def operation_by_key(self, idempotency_key: str) -> PaymentOperation | None:
        """Return the operation of this Payment with the idempotency key, or ``None``."""
        return next((op for op in self.operations if op.idempotency_key == idempotency_key), None)

    @property
    def has_pending_operation(self) -> bool:
        """Whether an operation of this Payment has no final result yet."""
        return any(not op.is_completed for op in self.operations)
