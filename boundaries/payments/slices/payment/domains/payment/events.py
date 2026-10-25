"""In-memory domain events recorded by the payments domain model."""

from dataclasses import dataclass

from .ids import OperationId, OperationKind, OrderId, PaymentId
from .vo.money import Money


@dataclass(frozen=True)
class PaymentAuthorized:
    """The Provider confirmed the Authorization of the full Payment amount."""

    payment_id: PaymentId
    order_id: OrderId
    amount: Money


@dataclass(frozen=True)
class PaymentCaptured:
    """The previously authorized amount was captured (full Capture)."""

    payment_id: PaymentId
    order_id: OrderId
    amount: Money


@dataclass(frozen=True)
class PaymentAuthorizationCancelled:
    """The Authorization was released without a Capture; the Payment is pending again."""

    payment_id: PaymentId
    order_id: OrderId
    amount: Money


@dataclass(frozen=True)
class PaymentRefunded:
    """The captured amount was returned in full."""

    payment_id: PaymentId
    order_id: OrderId
    amount: Money


@dataclass(frozen=True)
class PaymentFailed:
    """The Payment reached the terminal ``failed`` state, e.g. after a declined Authorization."""

    payment_id: PaymentId
    order_id: OrderId
    reason: str


@dataclass(frozen=True)
class PaymentOperationDeclined:
    """The Provider declined a Payment Operation; the reason is the Provider's decline reason."""

    operation_id: OperationId
    payment_id: PaymentId
    kind: OperationKind
    reason: str


PaymentEvent = (
    PaymentAuthorized
    | PaymentCaptured
    | PaymentAuthorizationCancelled
    | PaymentRefunded
    | PaymentFailed
)

PaymentDomainEvent = PaymentEvent | PaymentOperationDeclined
