"""Storage of payments: Unit of Work, repositories, queries, Outbox, Inbox.

``migrations/*.sql`` is the schema of the tables shared by every slice — the Transactional
Outbox ``payments_outbox`` with its publication and the Inbox ``inbox``; the set is applied
before the slice sets (``di/resources.py``).
"""

from .database import PostgresDatabase, PostgresUnitOfWork, create_database
from .migrate import MigrationSet, MissingMigrationsError, migrate
from .outbox import (
    EVENT_ID_NAMESPACE,
    PAYMENT_EVENTS_TOPIC,
    NoPublishedContracts,
    OutboxEventMapper,
    OutboxMessage,
    outbox_event_id,
)
from .queries import PostgresPaymentOperationHistory, PostgresPendingOperationQuery
from .repositories import (
    PostgresPaymentOperationRepository,
    PostgresPaymentOutbox,
    PostgresPaymentRepository,
    PostgresProviderMessageInbox,
)

__all__ = [
    "EVENT_ID_NAMESPACE",
    "PAYMENT_EVENTS_TOPIC",
    "MigrationSet",
    "MissingMigrationsError",
    "NoPublishedContracts",
    "OutboxEventMapper",
    "OutboxMessage",
    "PostgresDatabase",
    "PostgresPaymentOperationHistory",
    "PostgresPaymentOperationRepository",
    "PostgresPaymentOutbox",
    "PostgresPaymentRepository",
    "PostgresPendingOperationQuery",
    "PostgresProviderMessageInbox",
    "PostgresUnitOfWork",
    "create_database",
    "migrate",
    "outbox_event_id",
]
