"""In-memory domain events recorded by the payments domain model."""

from dataclasses import dataclass
from typing import Union

from .ids import OperationId, OperationKind, OrderId, PaymentId
from .vo.money import Money


@dataclass(frozen=True)
class PaymentAuthorized:
    payment_id: PaymentId
    order_id: OrderId
    amount: Money


@dataclass(frozen=True)
class PaymentCaptured:
    payment_id: PaymentId
    order_id: OrderId
    amount: Money


@dataclass(frozen=True)
class PaymentAuthorizationCancelled:
    payment_id: PaymentId
    order_id: OrderId
    amount: Money


@dataclass(frozen=True)
class PaymentRefunded:
    payment_id: PaymentId
    order_id: OrderId
    amount: Money


@dataclass(frozen=True)
class PaymentFailed:
    payment_id: PaymentId
    order_id: OrderId
    reason: str


@dataclass(frozen=True)
class PaymentOperationDeclined:
    operation_id: OperationId
    payment_id: PaymentId
    kind: OperationKind
    reason: str


PaymentEvent = Union[
    PaymentAuthorized,
    PaymentCaptured,
    PaymentAuthorizationCancelled,
    PaymentRefunded,
    PaymentFailed,
]

PaymentDomainEvent = Union[PaymentEvent, PaymentOperationDeclined]
