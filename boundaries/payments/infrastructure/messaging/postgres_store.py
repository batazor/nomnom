"""Postgres side of the payments Outbox relay: queries and the advisory lock session."""

import contextlib
import json
import logging
from datetime import datetime

import psycopg

from infrastructure.postgres import PostgresDatabase

from .outbox import OutboxRecord, TopicBacklog

logger = logging.getLogger(__name__)

# Constant key of the payments relay lock: ASCII "payments" as a signed 64-bit integer.
ADVISORY_LOCK_KEY = int.from_bytes(b"payments", "big", signed=True)

_MAX_ERROR_LENGTH = 1000


class PostgresOutboxStore:
    """Outbox queries plus a dedicated session connection holding the advisory lock."""

    def __init__(self, database: PostgresDatabase, dsn: str) -> None:
        self._db = database
        self._dsn = dsn
        self._lock_conn: psycopg.Connection | None = None

    def try_lock(self) -> bool:
        """Keep or take the session advisory lock that makes this replica the active relay.

        A lost lock session is dropped and the lock is retried on a new connection.
        """
        conn = self._lock_conn
        if conn is not None:
            try:
                conn.execute("SELECT 1")
                return True
            except psycopg.Error:
                # Session lost: so is the lock; another replica may take over.
                logger.warning("outbox relay lock session lost", exc_info=True)
                self._close_lock_conn()
        conn = psycopg.connect(self._dsn, autocommit=True)
        try:
            row = conn.execute("SELECT pg_try_advisory_lock(%s)", (ADVISORY_LOCK_KEY,)).fetchone()
        except BaseException:
            conn.close()
            raise
        if row is not None and row[0]:
            self._lock_conn = conn
            logger.info("outbox relay lock acquired: this instance is active")
            return True
        conn.close()
        return False

    def unlock(self) -> None:
        """Release the advisory lock and close its session; failures are logged."""
        conn = self._lock_conn
        if conn is None:
            return
        try:
            conn.execute("SELECT pg_advisory_unlock(%s)", (ADVISORY_LOCK_KEY,))
        except psycopg.Error:
            logger.warning("outbox relay unlock failed", exc_info=True)
        self._close_lock_conn()

    def _close_lock_conn(self) -> None:
        conn, self._lock_conn = self._lock_conn, None
        if conn is not None:
            with contextlib.suppress(psycopg.Error):
                conn.close()

    def fetch_pending(self, limit: int) -> list[OutboxRecord]:
        """Return up to ``limit`` unpublished records in ``seq`` order."""
        with self._db.connection() as conn:
            rows = conn.execute(
                "SELECT seq, event_id, event_type, aggregate_type, aggregate_id, "
                "aggregate_version, occurred_at, topic, payload, metadata, attempts "
                "FROM outbox WHERE published_at IS NULL ORDER BY seq LIMIT %s",
                (limit,),
            ).fetchall()
        return [
            OutboxRecord(
                seq=int(seq),
                event_id=str(event_id),
                event_type=event_type,
                aggregate_type=aggregate_type,
                aggregate_id=aggregate_id,
                aggregate_version=int(version),
                occurred_at=occurred_at,
                topic=topic,
                payload=bytes(payload),
                metadata=_metadata(metadata),
                attempts=int(attempts),
            )
            for (
                seq,
                event_id,
                event_type,
                aggregate_type,
                aggregate_id,
                version,
                occurred_at,
                topic,
                payload,
                metadata,
                attempts,
            ) in rows
        ]

    def mark_published(self, seq: int) -> None:
        """Set ``published_at`` so the record is not relayed again."""
        with self._db.connection() as conn:
            conn.execute("UPDATE outbox SET published_at = now() WHERE seq = %s", (seq,))

    def record_failure(self, seq: int, error: str) -> None:
        """Increment ``attempts`` and store ``error`` truncated to 1000 characters."""
        with self._db.connection() as conn:
            conn.execute(
                "UPDATE outbox SET attempts = attempts + 1, last_error = %s WHERE seq = %s",
                (error[:_MAX_ERROR_LENGTH], seq),
            )

    def delete_published_before(self, cutoff: datetime) -> int:
        """Delete records published before ``cutoff``; unpublished ones are kept."""
        with self._db.connection() as conn:
            cursor = conn.execute(
                "DELETE FROM outbox WHERE published_at IS NOT NULL AND published_at < %s",
                (cutoff,),
            )
            return cursor.rowcount

    def backlog(self) -> list[TopicBacklog]:
        """Return the count and oldest ``occurred_at`` of unpublished records per topic."""
        with self._db.connection() as conn:
            rows = conn.execute(
                "SELECT topic, count(*), min(occurred_at) FROM outbox "
                "WHERE published_at IS NULL GROUP BY topic"
            ).fetchall()
        return [TopicBacklog(topic, int(count), oldest) for topic, count, oldest in rows]


def _metadata(value: object) -> dict[str, str]:
    if isinstance(value, str):
        value = json.loads(value)
    if isinstance(value, dict):
        return {str(k): str(v) for k, v in value.items()}
    return {}
