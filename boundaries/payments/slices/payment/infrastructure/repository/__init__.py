"""Storage of the payment slice: repositories, queries, Outbox and Inbox adapters.

Adapters are named ``Database<Port>`` after the application or domain port they implement
over ``Database``, the pool of the payments database; names do not carry the storage
technology.

``migrations/*.sql`` owns ``payment_events``, ``payment_snapshots`` and the projections
``payments`` and ``payment_operations``; the migrations touch only these tables.
"""

from .event_codec import UnreadableStoredDataError
from .queries import (
    DatabasePaymentOperationHistory,
    DatabasePaymentStatusReader,
)
from .replay_metrics import ReplayMetrics
from .repositories import (
    DatabasePaymentOutbox,
    DatabasePaymentRepository,
    DatabaseProviderMessageInbox,
)

__all__ = [
    "DatabasePaymentOperationHistory",
    "DatabasePaymentOutbox",
    "DatabasePaymentRepository",
    "DatabasePaymentStatusReader",
    "DatabaseProviderMessageInbox",
    "ReplayMetrics",
    "UnreadableStoredDataError",
]
