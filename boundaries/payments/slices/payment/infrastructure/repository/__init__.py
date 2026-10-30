"""Storage of the payment slice: repositories, queries, Outbox and Inbox adapters.

Adapters are named ``Database<Port>`` after the application or domain port they implement
over ``Database``, the pool of the payments database; names do not carry the storage
technology.

``migrations/*.sql`` owns ``payments`` and ``payment_operations``; the migrations touch
only these tables.
"""

from .queries import (
    DatabasePaymentOperationHistory,
    DatabasePaymentStatusReader,
    DatabasePendingOperationQuery,
)
from .repositories import (
    DatabasePaymentOperationRepository,
    DatabasePaymentOutbox,
    DatabasePaymentRepository,
    DatabaseProviderMessageInbox,
)

__all__ = [
    "DatabasePaymentOperationHistory",
    "DatabasePaymentOperationRepository",
    "DatabasePaymentOutbox",
    "DatabasePaymentRepository",
    "DatabasePaymentStatusReader",
    "DatabasePendingOperationQuery",
    "DatabaseProviderMessageInbox",
]
