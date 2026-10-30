"""SQL of the Payment streams and their snapshots (local ADR 2).

Functions take the connection of the caller, so the events, the projections and the
snapshot of one commit share its savepoint. Writing a taken version raises the
``UniqueViolation`` of ``STREAM_VERSION_UNIQUE``.
"""

from collections.abc import Sequence
from dataclasses import dataclass

import psycopg
from psycopg.types.json import Jsonb

from pkg.telemetry import current_traceparent

from .event_codec import EncodedEvent, Payload

STREAM_VERSION_UNIQUE = "payment_events_pkey"


@dataclass(frozen=True)
class StoredEvent:
    """A row of ``payment_events``."""

    version: int
    event_type: str
    schema_version: int
    payload: Payload


@dataclass(frozen=True)
class StoredSnapshot:
    """A row of ``payment_snapshots``."""

    version: int
    schema_version: int
    state: Payload


def append_events(
    conn: psycopg.Connection, payment_id: str, after_version: int, events: Sequence[EncodedEvent]
) -> None:
    """Insert ``events`` at the versions right after ``after_version``."""
    traceparent = current_traceparent()
    with conn.cursor() as cursor:
        cursor.executemany(
            "INSERT INTO payment_events (payment_id, version, event_type, schema_version, "
            "payload, traceparent, recorded_at) VALUES (%s, %s, %s, %s, %s, %s, now())",
            [
                (
                    payment_id,
                    after_version + offset,
                    event.event_type,
                    event.schema_version,
                    Jsonb(event.payload),
                    traceparent,
                )
                for offset, event in enumerate(events, start=1)
            ],
        )


def read_events(conn: psycopg.Connection, payment_id: str, after_version: int) -> list[StoredEvent]:
    """Return the events of the stream after ``after_version`` in version order."""
    rows = conn.execute(
        "SELECT version, event_type, schema_version, payload FROM payment_events "
        "WHERE payment_id = %s AND version > %s ORDER BY version",
        (payment_id, after_version),
    ).fetchall()
    return [StoredEvent(int(v), event_type, int(s), payload) for v, event_type, s, payload in rows]


def read_snapshot(conn: psycopg.Connection, payment_id: str) -> StoredSnapshot | None:
    """Return the latest snapshot of the Payment, or ``None``."""
    row = conn.execute(
        "SELECT version, schema_version, state FROM payment_snapshots WHERE payment_id = %s",
        (payment_id,),
    ).fetchone()
    return None if row is None else StoredSnapshot(int(row[0]), int(row[1]), row[2])


def write_snapshot(
    conn: psycopg.Connection, payment_id: str, version: int, schema_version: int, state: Payload
) -> None:
    """Replace the snapshot of the Payment unless a newer one is stored."""
    conn.execute(
        "INSERT INTO payment_snapshots (payment_id, version, schema_version, state, recorded_at) "
        "VALUES (%s, %s, %s, %s, now()) "
        "ON CONFLICT (payment_id) DO UPDATE SET version = EXCLUDED.version, "
        "schema_version = EXCLUDED.schema_version, state = EXCLUDED.state, "
        "recorded_at = EXCLUDED.recorded_at "
        "WHERE EXCLUDED.version > payment_snapshots.version",
        (payment_id, version, schema_version, Jsonb(state)),
    )
