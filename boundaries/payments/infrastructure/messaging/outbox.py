"""Outbox record, its Event Envelope headers and the relay's ports (ADR 31)."""

from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Protocol

# Header of the Watermill message UUID: Go consumers log it as the message id.
WATERMILL_UUID_HEADER = "_watermill_message_uuid"


@dataclass(frozen=True)
class OutboxRecord:
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


class OutboxStore(Protocol):
    def try_lock(self) -> bool: ...

    def unlock(self) -> None: ...

    def fetch_pending(self, limit: int) -> list[OutboxRecord]: ...

    def mark_published(self, seq: int) -> None: ...

    def record_failure(self, seq: int, error: str) -> None: ...

    def delete_published_before(self, cutoff: datetime) -> int: ...

    def backlog(self) -> list[TopicBacklog]: ...


class Publisher(Protocol):
    def publish(self, record: OutboxRecord) -> None:
        """Return after the broker confirmed the record; raise ``PublishError`` otherwise."""
        ...

    def close(self, timeout: float) -> None: ...
