"""Postgres side of the payments Outbox relay: queries and the advisory lock session."""

import contextlib
import json
import logging
import os
import socket
import threading
from datetime import datetime
from typing import LiteralString

import psycopg

from infrastructure.postgres import PostgresDatabase

from .outbox import OutboxRecord, TopicBacklog

logger = logging.getLogger(__name__)

# Constant key of the payments relay lock: ASCII "payments" as a signed 64-bit integer.
ADVISORY_LOCK_KEY = int.from_bytes(b"payments", "big", signed=True)

_MAX_ERROR_LENGTH = 1000

# Deadline of one query on the lock session (seconds). A half-open TCP connection
# (peer gone without FIN/RST) must not block the ownership check for minutes.
LOCK_QUERY_TIMEOUT = 5.0

# libpq options of the lock session: the kernel detects a dead peer of an idle session
# in about keepalives_idle + keepalives_interval * keepalives_count seconds, and
# tcp_user_timeout (ms; Linux only) bounds unacknowledged writes.
_LOCK_CONNECT_OPTIONS: dict[str, int] = {
    "connect_timeout": 5,
    "keepalives": 1,
    "keepalives_idle": 10,
    "keepalives_interval": 5,
    "keepalives_count": 3,
    "tcp_user_timeout": 15_000,
}

_HOLDS_LOCK_QUERY = (
    "SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype = 'advisory' "
    "AND pid = pg_backend_pid() AND granted "
    "AND database = (SELECT oid FROM pg_database WHERE datname = current_database()) "
    "AND classid::bigint = %s AND objid::bigint = %s AND objsubid = 1)"
)


def advisory_lock_key_parts(key: int) -> tuple[int, int]:
    """Split a bigint advisory lock key into ``pg_locks`` ``(classid, objid)``.

    ``pg_advisory_lock(bigint)`` stores the high 32 bits in ``classid`` and the low
    32 bits in ``objid`` (both unsigned ``oid``) with ``objsubid = 1``.
    """
    unsigned = key & 0xFFFF_FFFF_FFFF_FFFF
    return unsigned >> 32, unsigned & 0xFFFF_FFFF


class LockQueryTimeoutError(Exception):
    """A query on the lock session missed its client-side deadline."""


class PostgresOutboxStore:
    """Outbox queries plus a dedicated session connection holding the advisory lock."""

    def __init__(
        self,
        database: PostgresDatabase,
        dsn: str,
        *,
        lock_query_timeout: float = LOCK_QUERY_TIMEOUT,
    ) -> None:
        self._db = database
        self._dsn = dsn
        self._lock_query_timeout = lock_query_timeout
        self._lock_conn: psycopg.Connection | None = None

    def try_lock(self) -> bool:
        """Keep or take the session advisory lock that makes this replica the active relay.

        A lost lock session is dropped and the lock is retried on a new connection.
        """
        if self._lock_conn is not None and self.holds_lock():
            return True
        conn = psycopg.connect(self._dsn, autocommit=True, **_LOCK_CONNECT_OPTIONS)
        try:
            # Server-side bound as well; the client-side deadline covers a dead network.
            _bounded_fetchone(
                conn,
                "SELECT set_config('statement_timeout', %s, false)",
                (f"{max(1, int(self._lock_query_timeout * 1000))}ms",),
                self._lock_query_timeout,
            )
            row = _bounded_fetchone(
                conn,
                "SELECT pg_try_advisory_lock(%s)",
                (ADVISORY_LOCK_KEY,),
                self._lock_query_timeout,
            )
        except BaseException:
            conn.close()
            raise
        if row is not None and row[0]:
            self._lock_conn = conn
            logger.info("outbox relay lock acquired: this instance is active")
            return True
        conn.close()
        return False

    def holds_lock(self) -> bool:
        """Check in ``pg_locks`` that the lock session still owns the advisory lock.

        The query is bounded by TCP keepalives, ``statement_timeout`` and a client-side
        deadline. An error, a missed deadline or a missing lock means the lock is lost:
        the session is dropped and another replica may take over.
        """
        conn = self._lock_conn
        if conn is None:
            return False
        try:
            row = _bounded_fetchone(
                conn,
                _HOLDS_LOCK_QUERY,
                advisory_lock_key_parts(ADVISORY_LOCK_KEY),
                self._lock_query_timeout,
            )
        except psycopg.Error, LockQueryTimeoutError:
            logger.warning("outbox relay lock session lost", exc_info=True)
            self._close_lock_conn()
            return False
        if row is None or not row[0]:
            logger.warning("outbox relay lock no longer held by its session")
            self._close_lock_conn()
            return False
        return True

    def unlock(self) -> None:
        """Release the advisory lock and close its session; failures are logged."""
        conn = self._lock_conn
        if conn is None:
            return
        try:
            _bounded_fetchone(
                conn,
                "SELECT pg_advisory_unlock(%s)",
                (ADVISORY_LOCK_KEY,),
                self._lock_query_timeout,
            )
        except psycopg.Error, LockQueryTimeoutError:
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


def _bounded_fetchone(
    conn: psycopg.Connection,
    query: LiteralString,
    params: tuple[int | str, ...],
    timeout: float,
) -> tuple[object, ...] | None:
    """Run ``query`` and fetch one row, failing after ``timeout`` seconds.

    On the deadline the socket is shut down, which wakes libpq at once; the connection
    is unusable afterwards and the caller drops it.

    Raises:
        LockQueryTimeoutError: the deadline passed, even if the query then finished.
        psycopg.Error: the query failed.
    """
    guard = threading.Lock()
    finished = False
    fired = False

    def _expire() -> None:
        nonlocal fired
        with guard:
            if finished:
                return
            fired = True
        _shutdown_socket(conn)

    timer = threading.Timer(timeout, _expire)
    timer.daemon = True
    timer.start()
    try:
        row = conn.execute(query, params).fetchone()
    except psycopg.Error as error:
        if fired:
            raise LockQueryTimeoutError(f"lock session query exceeded {timeout}s") from error
        raise
    finally:
        with guard:
            finished = True
        timer.cancel()
    if fired:
        raise LockQueryTimeoutError(f"lock session query exceeded {timeout}s")
    return row


def _shutdown_socket(conn: psycopg.Connection) -> None:
    # Дубликат дескриптора: shutdown рвёт общий сокет, а свой дескриптор libpq закроет сам.
    with contextlib.suppress(OSError, psycopg.Error):
        dup = socket.socket(fileno=os.dup(conn.pgconn.socket))
        try:
            dup.shutdown(socket.SHUT_RDWR)
        finally:
            dup.close()


def _metadata(value: object) -> dict[str, str]:
    if isinstance(value, str):
        value = json.loads(value)
    if isinstance(value, dict):
        return {str(k): str(v) for k, v in value.items()}
    return {}
