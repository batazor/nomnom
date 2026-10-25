"""Outbox record, its Event Envelope headers and the relay's ports by role (ADR 31)."""

from collections.abc import Callable
from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Protocol

# Header of the Watermill message UUID: Go consumers log it as the message id.
WATERMILL_UUID_HEADER = "_watermill_message_uuid"


@dataclass(frozen=True)
class OutboxRecord:
    """An unpublished Integration Event row of the ``outbox`` table; ``seq`` orders publishing."""

    seq: int
    event_id: str
    event_type: str
    aggregate_type: str
    aggregate_id: str
    aggregate_version: int
    occurred_at: datetime
    topic: str
    payload: bytes
    metadata: dict[str, str] = field(default_factory=dict)
    attempts: int = 0


@dataclass(frozen=True)
class TopicBacklog:
    """Unpublished records of one topic and the ``occurred_at`` of the oldest of them."""

    topic: str
    pending: int
    oldest_occurred_at: datetime | None


def format_rfc3339_nano(value: datetime) -> str:
    """UTC timestamp in Go ``time.RFC3339Nano``: trailing fractional zeros dropped, ``Z``."""
    value = value.astimezone(UTC)
    text = value.strftime("%Y-%m-%dT%H:%M:%S")
    if value.microsecond:
        text += f".{value.microsecond:06d}".rstrip("0")
    return text + "Z"


def kafka_headers(record: OutboxRecord) -> list[tuple[str, bytes]]:
    """Event Envelope headers, same format as ``orders/pkg/messaging/envelope.go``."""
    headers = [
        ("event_id", record.event_id),
        (WATERMILL_UUID_HEADER, record.event_id),
        ("event_type", record.event_type),
        ("occurred_at", format_rfc3339_nano(record.occurred_at)),
        ("aggregate_type", record.aggregate_type),
        ("aggregate_id", record.aggregate_id),
        ("aggregate_version", str(record.aggregate_version)),
    ]
    traceparent = record.metadata.get("traceparent")
    if traceparent:
        headers.append(("traceparent", traceparent))
    return [(name, value.encode()) for name, value in headers]


class PublishError(Exception):
    """A record was not confirmed by the broker."""

    def __init__(self, message: str, retriable: bool = True) -> None:
        super().__init__(message)
        self.retriable = retriable


class FatalPublishError(PublishError):
    """The producer is unusable (librdkafka fatal error): no later publish can succeed.

    The relay stops its worker so the process exits non-zero and is restarted.
    """

    def __init__(self, message: str) -> None:
        super().__init__(message, retriable=False)


class PublishInterruptedError(Exception):
    """Waiting for the delivery report was abandoned on shutdown; the record stays unpublished."""


class OutboxLease(Protocol):
    """Single-active-replica lock of the relay."""

    def try_lock(self) -> bool:
        """Hold or acquire the relay lock; ``False`` means this replica is standby."""
        ...

    def holds_lock(self) -> bool:
        """Check that the lock session taken by ``try_lock`` is still alive.

        ``False`` means the lock is lost (another replica may hold it now) or was never
        taken; a lost session is dropped, the next ``try_lock`` retries.
        """
        ...

    def unlock(self) -> None:
        """Release the relay lock if held; never raises on a lost session."""
        ...


class OutboxQueue(Protocol):
    """Unpublished records in ``seq`` order and the result of publishing each of them."""

    def fetch_pending(self, limit: int) -> list[OutboxRecord]:
        """Return up to ``limit`` unpublished records in ``seq`` order."""
        ...

    def mark_published(self, seq: int) -> None:
        """Mark the record as confirmed by the broker."""
        ...

    def record_failure(self, seq: int, error: str) -> None:
        """Count a failed publish attempt and keep the (truncated) error."""
        ...


class OutboxRetention(Protocol):
    """Removal of records published long enough ago."""

    def delete_published_before(self, cutoff: datetime) -> int:
        """Delete records published before ``cutoff`` and return their number."""
        ...


class OutboxBacklog(Protocol):
    """Per-topic backlog of unpublished records for the relay metrics."""

    def backlog(self) -> list[TopicBacklog]:
        """Return the per-topic backlog of unpublished records."""
        ...


class OutboxStore(OutboxLease, OutboxQueue, OutboxRetention, OutboxBacklog, Protocol):
    """Every role one relay cycle uses; the PostgreSQL store implements all of them.

    Components that need one role depend on that role only: ``RelayWorker`` releases the
    lock through ``OutboxLease`` (AC-640).
    """


class Publisher(Protocol):
    """Broker port of the relay."""

    def publish(self, record: OutboxRecord, should_stop: Callable[[], bool]) -> None:
        """Return after the broker confirmed the record.

        Raises:
            PublishError: the broker did not confirm the record; ``FatalPublishError``
                when the producer can no longer publish anything.
            PublishInterruptedError: ``should_stop`` turned true before the report.
        """
        ...

    def close(self, timeout: float) -> None:
        """Flush in-flight records within ``timeout`` seconds and release the client."""
        ...
