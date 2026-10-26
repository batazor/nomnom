"""psycopg 3 connection pool and the PostgreSQL Unit of Work.

The connection of the active Unit of Work is kept in a context variable: every
repository call made inside ``PostgresUnitOfWork.run`` uses it and joins that local
transaction. Outside a Unit of Work a call takes its own pooled connection and
commits on return.

The outer Unit of Work retries its work on a conflict (ADR 20): serialization failure,
deadlock, another transaction rollback (SQLSTATE class 40) or a version conflict of an
aggregate loaded inside the transaction. Retries are bounded by ``MAX_ATTEMPTS`` and
``RETRY_DEADLINE`` with exponential backoff. A failed commit is never retried: its
result is unknown and is resolved by the idempotency of the operation.
"""

import logging
import math
import time
from collections.abc import Callable, Iterator
from contextlib import AbstractContextManager, contextmanager
from contextvars import ContextVar
from datetime import timedelta
from typing import TypeVar

import psycopg
from opentelemetry import trace
from psycopg_pool import ConnectionPool

from pkg.config import DatabaseSettings

from .pool_metrics import PoolMetrics
from .statements import StatementTelemetry
from .transactions import (
    REASON,
    RetryReason,
    TransactionMetrics,
    TransactionOutcome,
    retry_reason,
)

T = TypeVar("T")

logger = logging.getLogger(__name__)

# Попыток одного Unit of Work, включая первую; следующая не начинается после дедлайна
# от начала первой. Backoff удваивается: 10 ms, 20 ms.
MAX_ATTEMPTS = 3
RETRY_DEADLINE = timedelta(seconds=1)
RETRY_BACKOFF = timedelta(milliseconds=10)

# db.client.connection.pool.name пула процесса.
POOL_NAME = "payments"

_active: ContextVar[psycopg.Connection | None] = ContextVar(
    "payments_postgres_connection", default=None
)


class PostgresDatabase:
    """Connection pool of the payments database, aware of the active Unit of Work."""

    def __init__(self, pool: ConnectionPool, pool_metrics: PoolMetrics | None = None) -> None:
        self._pool = pool
        self._metrics = pool_metrics or PoolMetrics(POOL_NAME)
        self._metrics.bind(pool)

    def borrow(self) -> AbstractContextManager[psycopg.Connection]:
        """Borrow a pooled connection: commit on exit, roll back on error, measured."""
        return self._metrics.borrow()

    @contextmanager
    def connection(self) -> Iterator[psycopg.Connection]:
        """Yield the active Unit of Work connection, or a pooled one committed on exit."""
        active = _active.get()
        if active is not None:
            yield active
            return
        with self.borrow() as conn:
            yield conn

    def check(self, timeout: float = 5.0) -> None:
        """Fail fast at startup when the database is unreachable."""
        with self._pool.connection(timeout=timeout) as conn:
            conn.execute("SELECT 1")

    def close(self) -> None:
        """Close the pool and all its connections."""
        self._pool.close()


class PostgresUnitOfWork:
    """One local PostgreSQL transaction per use case step; nested calls join it.

    Every attempt of the outer Unit of Work is recorded by ``TransactionMetrics``.
    """

    def __init__(
        self,
        database: PostgresDatabase,
        transaction_metrics: TransactionMetrics | None = None,
        sleep: Callable[[float], None] = time.sleep,
    ) -> None:
        self._database = database
        self._metrics = transaction_metrics or TransactionMetrics()
        self._sleep = sleep

    def run(self, work: Callable[[], T]) -> T:
        """Run ``work`` in one transaction: commit on return, roll back and re-raise on error.

        A conflict rolls the attempt back and runs ``work`` again in a new transaction,
        so ``work`` must touch only the local storage (``UnitOfWork``).
        """
        if _active.get() is not None:
            return work()
        started = time.monotonic()
        attempt = 1
        while True:
            try:
                return self._attempt(work)
            except _WorkFailedError as failed:
                error = failed.error
            reason = retry_reason(error)
            backoff = RETRY_BACKOFF.total_seconds() * 2 ** (attempt - 1)
            deadline = started + RETRY_DEADLINE.total_seconds()
            if reason is None or attempt >= MAX_ATTEMPTS or time.monotonic() + backoff > deadline:
                raise error
            self._retried(reason, attempt)
            self._sleep(backoff)
            attempt += 1

    def _attempt(self, work: Callable[[], T]) -> T:
        """One transaction; an exception of ``work`` comes out as ``_WorkFailedError``."""
        with self._database.borrow() as conn:
            started = time.perf_counter()
            outcome = TransactionOutcome.ROLLED_BACK
            try:
                with conn.transaction():
                    token = _active.set(conn)
                    try:
                        result = work()
                    except Exception as error:
                        raise _WorkFailedError(error) from error
                    finally:
                        _active.reset(token)
                    # Выход из блока выполняет COMMIT: его ошибка — commit_failed.
                    outcome = TransactionOutcome.COMMIT_FAILED
                outcome = TransactionOutcome.COMMITTED
                return result
            finally:
                self._metrics.finished(outcome, time.perf_counter() - started)

    def _retried(self, reason: RetryReason, attempt: int) -> None:
        self._metrics.retried(reason)
        trace.get_current_span().add_event(
            "nomnom.uow.retry", {REASON: reason.value, "nomnom.uow.attempt": attempt + 1}
        )
        logger.warning(
            "unit of work retried after %s (attempt %d of %d)",
            reason.value,
            attempt + 1,
            MAX_ATTEMPTS,
            extra={"operation": "unit_of_work", "outcome": "retry"},
        )


class _WorkFailedError(Exception):
    """Carries an exception of the work out of the transaction block, after its rollback."""

    def __init__(self, error: Exception) -> None:
        super().__init__(error)
        self.error = error


def create_database(
    settings: DatabaseSettings, statement_telemetry: StatementTelemetry | None = None
) -> PostgresDatabase:
    """Create the pool without connecting; ``check`` verifies reachability.

    Limits come from ``DatabaseSettings``: behind the session-mode pooler every pooled
    connection pins a server connection, and psycopg's automatic prepared statements need
    session mode.
    """
    pool_metrics = PoolMetrics(POOL_NAME)
    statements = statement_telemetry or StatementTelemetry()
    pool = ConnectionPool(
        settings.url,
        connection_class=pool_metrics.connection_class(),
        kwargs={
            "connect_timeout": connect_timeout_seconds(settings.connect_timeout),
            # Span и db.client.operation.duration каждого statement.
            "cursor_factory": statements.cursor_class(),
        },
        min_size=settings.min_conns,
        max_size=settings.max_conns,
        open=False,
        name=POOL_NAME,
        check=ConnectionPool.check_connection,
        timeout=settings.acquire_timeout.total_seconds(),
        max_lifetime=settings.max_conn_lifetime.total_seconds(),
        max_idle=settings.max_conn_idle_time.total_seconds(),
    )
    pool.open(wait=False)
    return PostgresDatabase(pool, pool_metrics)


def connect_timeout_seconds(timeout: timedelta) -> int:
    """Round a connect timeout up to the whole seconds of libpq ``connect_timeout``."""
    return max(1, math.ceil(timeout.total_seconds()))
