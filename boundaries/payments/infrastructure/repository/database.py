"""psycopg 3 connection pool and the PostgreSQL Unit of Work.

The connection of the active Unit of Work is kept in a context variable: every
repository call made inside ``PostgresUnitOfWork.run`` uses it and joins that local
transaction. Outside a Unit of Work a call takes its own pooled connection and
commits on return.
"""

import math
from collections.abc import Callable, Iterator
from contextlib import AbstractContextManager, contextmanager
from contextvars import ContextVar
from datetime import timedelta
from typing import TypeVar

import psycopg
from psycopg_pool import ConnectionPool

from pkg.config import DatabaseSettings

from .pool_metrics import PoolMetrics

T = TypeVar("T")

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
    """One local PostgreSQL transaction per use case step; nested calls join it."""

    def __init__(self, database: PostgresDatabase) -> None:
        self._database = database

    def run(self, work: Callable[[], T]) -> T:
        """Run ``work`` in one transaction: commit on return, roll back and re-raise on error."""
        if _active.get() is not None:
            return work()
        with self._database.borrow() as conn, conn.transaction():
            token = _active.set(conn)
            try:
                return work()
            finally:
                _active.reset(token)


def create_database(settings: DatabaseSettings) -> PostgresDatabase:
    """Create the pool without connecting; ``check`` verifies reachability.

    Limits come from ``DatabaseSettings``: behind the session-mode pooler every pooled
    connection pins a server connection, and psycopg's automatic prepared statements need
    session mode.
    """
    pool_metrics = PoolMetrics(POOL_NAME)
    pool = ConnectionPool(
        settings.url,
        connection_class=pool_metrics.connection_class(),
        kwargs={"connect_timeout": connect_timeout_seconds(settings.connect_timeout)},
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
