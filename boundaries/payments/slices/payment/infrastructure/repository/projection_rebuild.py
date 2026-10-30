"""Rebuild and check of the data derived from the Payment streams (local ADR 2).

``rebuild`` replaces ``payments`` and ``payment_operations``, and on request
``payment_snapshots``, in one transaction under ``EXCLUSIVE`` table locks: queries keep
reading the old rows until the commit and writers wait, so the uniqueness of ``order_id``
and ``idempotency_key`` is never unprotected. Streams are read in full, never from a
snapshot, otherwise a broken snapshot would reach the projections. A writer that has
already appended its uncommitted events waits at the projection lock and overwrites its
row after the commit. The Outbox is not touched, so nothing is published again.
"""

from collections.abc import Sequence
from dataclasses import dataclass
from enum import StrEnum
from typing import Any

import psycopg

from infrastructure.repository.database import Database
from slices.payment.domains.payment import PaymentModel

from .event_codec import (
    SNAPSHOT_SCHEMA_VERSION,
    UnreadableStoredDataError,
    decode_snapshot,
    encode_snapshot,
)
from .event_store import read_events, write_snapshot
from .repositories import (
    OPERATION_COLUMNS,
    PAYMENT_COLUMNS,
    decode_stream,
    operation_row,
    payment_row,
    project_payment,
    restore_payment,
)

# Запись в payments стоит, пока идёт пересборка: чужая длинная транзакция не должна
# подвесить команду, поэтому ожидание блокировки ограничено.
LOCK_TIMEOUT = "5s"


class ProjectionRebuildError(RuntimeError):
    """The rebuilt rows do not match the streams; the transaction is rolled back."""


@dataclass(frozen=True)
class RebuildResult:
    """Rows written by a committed rebuild; ``snapshots`` is 0 unless they were rebuilt."""

    payments: int
    operations: int
    snapshots: int


class MismatchKind(StrEnum):
    """How stored derived data differs from what the streams say."""

    MISSING = "missing"
    EXTRA = "extra"
    DIFFERS = "differs"
    UNREADABLE = "unreadable"


@dataclass(frozen=True)
class ProjectionMismatch:
    """A derived row that differs from its stream; values stay out, they hold provider data."""

    table: str
    key: str
    kind: MismatchKind


class ProjectionRebuilder:
    """Operator commands over the derived data of the Payment streams."""

    def __init__(
        self, database: Database, snapshot_every: int, lock_timeout: str = LOCK_TIMEOUT
    ) -> None:
        self._db = database
        self._snapshot_every = snapshot_every
        self._lock_timeout = lock_timeout

    def rebuild(self, *, snapshots: bool) -> RebuildResult:
        """Replace the projections, and ``payment_snapshots`` on request, from the streams.

        Everything is rolled back on an error.

        Raises:
            psycopg.errors.LockNotAvailable: A table stayed locked for ``lock_timeout``.
            UnreadableStoredDataError: A stream cannot be read.
            ProjectionRebuildError: The written rows do not match the streams.
        """
        with self._db.borrow() as conn:
            conn.execute("SELECT set_config('lock_timeout', %s, true)", (self._lock_timeout,))
            conn.execute("LOCK TABLE payments, payment_operations IN EXCLUSIVE MODE")
            if snapshots:
                conn.execute("LOCK TABLE payment_snapshots IN EXCLUSIVE MODE")
                conn.execute("DELETE FROM payment_snapshots")
            conn.execute("DELETE FROM payment_operations")
            conn.execute("DELETE FROM payments")
            result = self._write(conn, snapshots=snapshots)
            self._verify(conn, result)
            return result

    def _write(self, conn: psycopg.Connection, *, snapshots: bool) -> RebuildResult:
        payments = operations = written_snapshots = 0
        for payment_id in stream_ids(conn):
            events = decode_stream(payment_id, 0, read_events(conn, payment_id, 0))
            payment = restore_payment(payment_id, None, 0, events)
            project_payment(conn, payment.model, payment.version)
            payments += 1
            operations += len(payment.operations)
            boundary = payment.version // self._snapshot_every * self._snapshot_every
            if snapshots and boundary > 0:
                state = restore_payment(payment_id, None, 0, events[:boundary])
                write_snapshot(
                    conn,
                    payment_id,
                    boundary,
                    SNAPSHOT_SCHEMA_VERSION,
                    encode_snapshot(state.model),
                )
                written_snapshots += 1
        return RebuildResult(payments, operations, written_snapshots)

    @staticmethod
    def _verify(conn: psycopg.Connection, expected: RebuildResult) -> None:
        for table, rows, wanted in (
            ("payments", _count(conn, "SELECT count(*) FROM payments"), expected.payments),
            (
                "payment_operations",
                _count(conn, "SELECT count(*) FROM payment_operations"),
                expected.operations,
            ),
        ):
            if rows != wanted:
                raise ProjectionRebuildError(f"{table} has {rows} rows, streams say {wanted}")

    def check(self) -> list[ProjectionMismatch]:
        """Compare the derived data with the streams without changing anything.

        Reads one snapshot of the database, so concurrent writers cause no false differences.
        A snapshot of another schema version is skipped: it is not used on load.

        Raises:
            UnreadableStoredDataError: A stream cannot be read.
        """
        with self._db.borrow() as conn:
            conn.execute("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
            mismatches: list[ProjectionMismatch] = []
            stored_payments = _rows(conn, f"SELECT {PAYMENT_COLUMNS} FROM payments")  # noqa: S608 - only fixed column/condition constants are interpolated
            stored_operations = _rows(conn, f"SELECT {OPERATION_COLUMNS} FROM payment_operations")  # noqa: S608 - only fixed column/condition constants are interpolated
            snapshots = {
                str(row[0]): row
                for row in conn.execute(
                    "SELECT payment_id, version, schema_version, state FROM payment_snapshots"
                ).fetchall()
            }
            expected_payments: dict[str, tuple[Any, ...]] = {}
            expected_operations: dict[str, tuple[Any, ...]] = {}
            for payment_id in stream_ids(conn):
                events = decode_stream(payment_id, 0, read_events(conn, payment_id, 0))
                payment = restore_payment(payment_id, None, 0, events)
                expected_payments[payment_id] = payment_row(payment.model, payment.version)
                for operation in payment.operations:
                    expected_operations[operation.id] = operation_row(operation)
                snapshot = snapshots.get(payment_id)
                if snapshot is not None:
                    mismatches.extend(_check_snapshot(payment_id, snapshot, events))
            mismatches.extend(
                ProjectionMismatch("payment_snapshots", payment_id, MismatchKind.EXTRA)
                for payment_id in snapshots.keys() - expected_payments.keys()
            )
            mismatches.extend(_compare("payments", expected_payments, stored_payments))
            mismatches.extend(
                _compare("payment_operations", expected_operations, stored_operations)
            )
            return mismatches


def stream_ids(conn: psycopg.Connection) -> list[str]:
    """Identifiers of every Payment with a stream, in a stable order."""
    rows = conn.execute("SELECT DISTINCT payment_id FROM payment_events ORDER BY 1").fetchall()
    return [str(row[0]) for row in rows]


def _check_snapshot(
    payment_id: str, snapshot: tuple[Any, ...], events: Sequence[Any]
) -> list[ProjectionMismatch]:
    _, version, schema_version, state = snapshot
    if schema_version != SNAPSHOT_SCHEMA_VERSION:
        return []
    try:
        model: PaymentModel = decode_snapshot(state)
    except UnreadableStoredDataError:
        return [ProjectionMismatch("payment_snapshots", payment_id, MismatchKind.UNREADABLE)]
    if (
        version > len(events)
        or model != restore_payment(payment_id, None, 0, events[:version]).model
    ):
        return [ProjectionMismatch("payment_snapshots", payment_id, MismatchKind.DIFFERS)]
    return []


def _compare(
    table: str, expected: dict[str, tuple[Any, ...]], stored: dict[str, tuple[Any, ...]]
) -> list[ProjectionMismatch]:
    mismatches = [
        ProjectionMismatch(table, key, MismatchKind.MISSING) for key in expected.keys() - stored
    ]
    mismatches += [
        ProjectionMismatch(table, key, MismatchKind.EXTRA) for key in stored.keys() - expected
    ]
    mismatches += [
        ProjectionMismatch(table, key, MismatchKind.DIFFERS)
        for key in expected.keys() & stored.keys()
        if expected[key] != stored[key]
    ]
    return sorted(mismatches, key=lambda mismatch: mismatch.key)


def _rows(conn: psycopg.Connection, query: Any) -> dict[str, tuple[Any, ...]]:
    return {str(row[0]): tuple(row) for row in conn.execute(query).fetchall()}


def _count(conn: psycopg.Connection, query: Any) -> int:
    row = conn.execute(query).fetchone()
    return 0 if row is None else int(row[0])
