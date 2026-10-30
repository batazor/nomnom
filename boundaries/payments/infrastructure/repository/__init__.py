"""Storage shared by the payments process: the pool, Unit of Work, migrations and Outbox.

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

__all__ = [
    "EVENT_ID_NAMESPACE",
    "PAYMENT_EVENTS_TOPIC",
    "Database",
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
