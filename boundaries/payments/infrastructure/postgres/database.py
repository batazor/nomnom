"""psycopg 3 connection pool and the PostgreSQL Unit of Work.

The connection of the active Unit of Work is kept in a context variable: every
repository call made inside ``PostgresUnitOfWork.run`` uses it and joins that local
transaction. Outside a Unit of Work a call takes its own pooled connection and
commits on return.
"""

from collections.abc import Iterator
from contextlib import contextmanager
from contextvars import ContextVar
from typing import Callable, Optional, TypeVar

import psycopg
from psycopg_pool import ConnectionPool

from pkg.config import DatabaseSettings

T = TypeVar("T")

_active: ContextVar[Optional[psycopg.Connection]] = ContextVar(
    "payments_postgres_connection", default=None
)


class PostgresDatabase:
    def __init__(self, pool: ConnectionPool) -> None:
        self._pool = pool

    @contextmanager
    def connection(self) -> Iterator[psycopg.Connection]:
        active = _active.get()
        if active is not None:
            yield active
            return
        with self._pool.connection() as conn:
            yield conn

    def check(self, timeout: float = 5.0) -> None:
        """Fail fast at startup when the database is unreachable."""
        with self._pool.connection(timeout=timeout) as conn:
            conn.execute("SELECT 1")

    def close(self) -> None:
        self._pool.close()


class PostgresUnitOfWork:
    """One local PostgreSQL transaction per use case step; nested calls join it."""

    def __init__(self, database: PostgresDatabase) -> None:
        self._database = database

    def run(self, work: Callable[[], T]) -> T:
        if _active.get() is not None:
            return work()
        with self._database._pool.connection() as conn, conn.transaction():
            token = _active.set(conn)
            try:
                return work()
            finally:
                _active.reset(token)


def create_database(settings: DatabaseSettings) -> PostgresDatabase:
    """Create the pool without connecting; ``check`` verifies reachability."""
    pool = ConnectionPool(
        settings.url,
        min_size=0,
        max_size=settings.max_conns,
        open=False,
        name="payments",
        check=ConnectionPool.check_connection,
    )
    pool.open(wait=False)
    return PostgresDatabase(pool)
