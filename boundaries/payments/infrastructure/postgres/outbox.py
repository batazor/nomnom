"""Outbox seam of ADR 31: Domain Event → Integration Event mapping and record identity.

``OutboxEventMapper`` is the extension point for the Protobuf mappers of
``nomnom/payments/v1/events.proto`` (topic ``payments.v1.payment-events``); the
entrypoint injects ``infrastructure.messaging.PaymentEventsMapper``. Factories default
to ``NoPublishedContracts``: no Domain Event has a contract, nothing is written.
"""

import uuid
from dataclasses import dataclass
from typing import Optional, Protocol

from opentelemetry import propagate

from pkg.application import AggregateRef
from slices.payment.domains.payment import PaymentDomainEvent

# ADR 31: namespace of deterministic Outbox event ids.
EVENT_ID_NAMESPACE = uuid.UUID("8f1d3c2a-5b7e-4a90-9c61-2e4f7b8d0a13")
PAYMENT_EVENTS_TOPIC = "payments.v1.payment-events"


@dataclass(frozen=True)
class OutboxMessage:
    """Integration Event ready for the Outbox."""

    # Full Protobuf message name, e.g. ``nomnom.payments.v1.PaymentAuthorized``.
    event_type: str
    topic: str
    # Serialized Protobuf message of the contract.
    payload: bytes


class OutboxEventMapper(Protocol):
    def map(self, aggregate: AggregateRef, event: PaymentDomainEvent) -> Optional[OutboxMessage]:
        """Return the Integration Event, or ``None`` when the event has no contract."""
        ...


class NoPublishedContracts:
    """Default mapper of the factories: no event reaches the Outbox."""

    def map(self, aggregate: AggregateRef, event: PaymentDomainEvent) -> Optional[OutboxMessage]:
        return None


def outbox_event_id(aggregate: AggregateRef, event_type: str) -> uuid.UUID:
    return uuid.uuid5(
        EVENT_ID_NAMESPACE,
        f"{aggregate.type}:{aggregate.id}:{aggregate.version}:{event_type}",
    )


def outbox_metadata() -> dict[str, str]:
    """``traceparent`` of the current span, when there is one."""
    carrier: dict[str, str] = {}
    propagate.inject(carrier)
    return {k: v for k, v in carrier.items() if k == "traceparent"}
