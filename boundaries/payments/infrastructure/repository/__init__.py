"""Storage of payments: Unit of Work, repositories, queries, Outbox, Inbox.

Adapters are named ``Database<Port>`` after the application or domain port they implement
over ``Database``, the pool of the payments database; names do not carry the storage
technology.

``migrations/*.sql`` is the schema of the tables shared by every slice — the Transactional
Outbox ``payments_outbox`` with its publication and the Inbox ``inbox``; the set is applied
before the slice sets (``di/resources.py``).
"""

from .database import Database, DatabaseUnitOfWork, create_database
from .migrate import MigrationSet, MissingMigrationsError, migrate
from .outbox import (
    EVENT_ID_NAMESPACE,
    PAYMENT_EVENTS_TOPIC,
    NoPublishedContracts,
    OutboxEventMapper,
    OutboxMessage,
    outbox_event_id,
)
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
    "EVENT_ID_NAMESPACE",
    "PAYMENT_EVENTS_TOPIC",
    "Database",
    "DatabasePaymentOperationHistory",
    "DatabasePaymentOperationRepository",
    "DatabasePaymentOutbox",
    "DatabasePaymentRepository",
    "DatabasePaymentStatusReader",
    "DatabasePendingOperationQuery",
    "DatabaseProviderMessageInbox",
    "DatabaseUnitOfWork",
    "MigrationSet",
    "MissingMigrationsError",
    "NoPublishedContracts",
    "OutboxEventMapper",
    "OutboxMessage",
    "create_database",
    "migrate",
    "outbox_event_id",
]
