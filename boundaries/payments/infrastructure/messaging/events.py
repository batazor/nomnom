"""Domain Event → Integration Event mapper of ``nomnom/payments/v1/events.proto``.

Published types are an explicit allowlist (ADR 31): a Domain Event without a contract,
such as ``PaymentOperationDeclined``, maps to ``None`` and never reaches the Outbox.
"""

import sys
from collections.abc import Callable
from pathlib import Path

from google.protobuf.message import Message

from infrastructure.repository.outbox import PAYMENT_EVENTS_TOPIC, OutboxMessage
from pkg.application import AggregateRef
from slices.payment.domains.payment import (
    Money,
    PaymentAuthorizationCancelled,
    PaymentAuthorized,
    PaymentCaptured,
    PaymentDomainEvent,
    PaymentFailed,
    PaymentRefunded,
)

_GEN_ROOT = Path(__file__).resolve().parent.parent / "grpc" / "gen"
if str(_GEN_ROOT) not in sys.path:
    sys.path.insert(0, str(_GEN_ROOT))

from nomnom.payments.v1 import events_pb2, payments_pb2  # noqa: E402


def _money(amount: Money) -> payments_pb2.Money:
    return payments_pb2.Money(amount=str(amount.amount), currency=amount.currency)


def _authorized(event: PaymentAuthorized) -> Message:
    return events_pb2.PaymentAuthorized(
        payment_id=str(event.payment_id),
        order_id=str(event.order_id),
        market_id=str(event.market_id),
        amount=_money(event.amount),
    )


def _captured(event: PaymentCaptured) -> Message:
    return events_pb2.PaymentCaptured(
        payment_id=str(event.payment_id),
        order_id=str(event.order_id),
        market_id=str(event.market_id),
        amount=_money(event.amount),
    )


def _cancelled(event: PaymentAuthorizationCancelled) -> Message:
    return events_pb2.PaymentAuthorizationCancelled(
        payment_id=str(event.payment_id),
        order_id=str(event.order_id),
        market_id=str(event.market_id),
        amount=_money(event.amount),
    )


def _refunded(event: PaymentRefunded) -> Message:
    return events_pb2.PaymentRefunded(
        payment_id=str(event.payment_id),
        order_id=str(event.order_id),
        market_id=str(event.market_id),
        amount=_money(event.amount),
    )


def _failed(event: PaymentFailed) -> Message:
    return events_pb2.PaymentFailed(
        payment_id=str(event.payment_id),
        order_id=str(event.order_id),
        market_id=str(event.market_id),
        reason=event.reason,
    )


# Explicit allowlist of published Domain Events.
_CONTRACTS: dict[type, Callable[..., Message]] = {
    PaymentAuthorized: _authorized,
    PaymentCaptured: _captured,
    PaymentAuthorizationCancelled: _cancelled,
    PaymentRefunded: _refunded,
    PaymentFailed: _failed,
}


def event_type_of(message: Message) -> str:
    """``event_type`` of a contract: its full Protobuf message name."""
    return message.DESCRIPTOR.full_name


class PaymentEventsMapper:
    """OutboxEventMapper of the ``payments.v1.payment-events`` topic."""

    def map(self, aggregate: AggregateRef, event: PaymentDomainEvent) -> OutboxMessage | None:
        """Build the Protobuf contract of ``event``; ``None`` when the event is not published."""
        build = _CONTRACTS.get(type(event))
        if build is None:
            return None
        message = build(event)
        return OutboxMessage(
            event_type_of(message), PAYMENT_EVENTS_TOPIC, message.SerializeToString()
        )
