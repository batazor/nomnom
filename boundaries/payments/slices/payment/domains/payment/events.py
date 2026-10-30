"""Domain events of a Payment: its stream of facts, the source of truth (local ADR 2).

The aggregate records them and the repository appends them to the stream. Only the facts
in the allowlist of the Outbox mapper are published as Integration Events.
"""

from dataclasses import dataclass
from datetime import datetime

from .ids import OperationId, OperationKind, OperationRequester, OrderId, PaymentId
from .vo.market import MarketId
from .vo.money import Money


@dataclass(frozen=True)
class PaymentCreated:
    """The local payment intent of an Order exists; the first event of every stream."""

    payment_id: PaymentId
    order_id: OrderId
    market_id: MarketId
    amount: Money


@dataclass(frozen=True)
class PaymentAuthorized:
    """The Provider confirmed the Authorization of the full Payment amount."""

    payment_id: PaymentId
    order_id: OrderId
    market_id: MarketId
    amount: Money
    provider_reference: str


@dataclass(frozen=True)
class PaymentCaptured:
    """The previously authorized amount was captured (full Capture)."""

    payment_id: PaymentId
    order_id: OrderId
    market_id: MarketId
    amount: Money


@dataclass(frozen=True)
class PaymentAuthorizationCancelled:
    """The Authorization was released without a Capture; the Payment is pending again."""

    payment_id: PaymentId
    order_id: OrderId
    market_id: MarketId
    amount: Money


@dataclass(frozen=True)
class PaymentRefunded:
    """The captured amount was returned in full."""

    payment_id: PaymentId
    order_id: OrderId
    market_id: MarketId
    amount: Money


@dataclass(frozen=True)
class PaymentFailed:
    """The Payment reached the terminal ``failed`` state, e.g. after a declined Authorization."""

    payment_id: PaymentId
    order_id: OrderId
    market_id: MarketId
    reason: str


@dataclass(frozen=True)
class PaymentOperationRequested:
    """A Payment Operation is recorded before the Provider is called.

    ``requested_by`` is ``None`` only in events recorded before the field existed: it is
    unknown who requested them.
    """

    operation_id: OperationId
    payment_id: PaymentId
    kind: OperationKind
    idempotency_key: str
    amount: Money
    requested_at: datetime
    requested_by: OperationRequester | None


@dataclass(frozen=True)
class PaymentOperationSucceeded:
    """The Provider confirmed a Payment Operation; it may return no Provider Reference."""

    operation_id: OperationId
    payment_id: PaymentId
    kind: OperationKind
    provider_reference: str | None
    completed_at: datetime


@dataclass(frozen=True)
class PaymentOperationDeclined:
    """The Provider declined a Payment Operation; the reason is the Provider's decline reason."""

    operation_id: OperationId
    payment_id: PaymentId
    kind: OperationKind
    reason: str
    completed_at: datetime


PaymentEvent = (
    PaymentAuthorized
    | PaymentCaptured
    | PaymentAuthorizationCancelled
    | PaymentRefunded
    | PaymentFailed
)

PaymentDomainEvent = (
    PaymentEvent
    | PaymentCreated
    | PaymentOperationRequested
    | PaymentOperationSucceeded
    | PaymentOperationDeclined
)
