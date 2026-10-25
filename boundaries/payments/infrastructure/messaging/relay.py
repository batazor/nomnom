"""Polling relay of the payments Outbox (ADR 31, observability of ADR 13).

One active instance holds a session-level ``pg_try_advisory_lock``; standby replicas
poll the lock only. Each cycle reads a batch of unpublished records in ``seq`` order,
publishes them one by one with an idempotent Kafka producer (``acks=all``) and marks
``published_at`` only after the broker's delivery report. The first failure stops the
batch (head-of-line): it gets ``attempts``/``last_error`` and the next cycle starts
from it again. Published records older than the retention are deleted.
"""

import json
import logging
import threading
import time
from collections.abc import Sequence
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Any, Callable, Optional, Protocol

import psycopg
from opentelemetry import metrics

from infrastructure.postgres import PostgresDatabase

logger = logging.getLogger(__name__)

# Constant key of the payments relay lock: ASCII "payments" as a signed 64-bit integer.
ADVISORY_LOCK_KEY = int.from_bytes(b"payments", "big", signed=True)

# Header of the Watermill message UUID: Go consumers log it as the message id.
WATERMILL_UUID_HEADER = "_watermill_message_uuid"

# Retention cleanup runs at most this often, not every poll.
CLEANUP_INTERVAL = timedelta(minutes=1)

# Upper bound of one delivery report wait; librdkafka retries inside it.
DEFAULT_DELIVERY_TIMEOUT = 30.0

_MAX_ERROR_LENGTH = 1000


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
    oldest_occurred_at: Optional[datetime]


def format_rfc3339_nano(value: datetime) -> str:
    """UTC timestamp in Go ``time.RFC3339Nano``: trailing fractional zeros dropped, ``Z``."""
    value = value.astimezone(timezone.utc)
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


class PostgresOutboxStore:
    """Outbox queries plus a dedicated session connection holding the advisory lock."""

    def __init__(self, database: PostgresDatabase, dsn: str) -> None:
        self._db = database
        self._dsn = dsn
        self._lock_conn: Optional[psycopg.Connection] = None

    def try_lock(self) -> bool:
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
            try:
                conn.close()
            except psycopg.Error:
                pass

    def fetch_pending(self, limit: int) -> list[OutboxRecord]:
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
        with self._db.connection() as conn:
            conn.execute("UPDATE outbox SET published_at = now() WHERE seq = %s", (seq,))

    def record_failure(self, seq: int, error: str) -> None:
        with self._db.connection() as conn:
            conn.execute(
                "UPDATE outbox SET attempts = attempts + 1, last_error = %s WHERE seq = %s",
                (error[:_MAX_ERROR_LENGTH], seq),
            )

    def delete_published_before(self, cutoff: datetime) -> int:
        with self._db.connection() as conn:
            cursor = conn.execute(
                "DELETE FROM outbox WHERE published_at IS NOT NULL AND published_at < %s",
                (cutoff,),
            )
            return cursor.rowcount

    def backlog(self) -> list[TopicBacklog]:
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


class KafkaPublisher:
    """Idempotent ``confluent-kafka`` producer; one record in flight per call."""

    def __init__(
        self,
        brokers: str,
        client_id: str,
        delivery_timeout: float = DEFAULT_DELIVERY_TIMEOUT,
        extra_config: Optional[dict[str, Any]] = None,
    ) -> None:
        from confluent_kafka import Producer

        self._delivery_timeout = delivery_timeout
        config: dict[str, Any] = {
            "bootstrap.servers": brokers,
            "client.id": client_id,
            "enable.idempotence": True,
            "acks": "all",
            # The delivery report arrives (success or error) before the wait bound.
            "delivery.timeout.ms": int(delivery_timeout * 1000),
        }
        config.update(extra_config or {})
        self._producer = Producer(config)

    def publish(self, record: OutboxRecord) -> None:
        from confluent_kafka import KafkaException

        report: dict[str, Any] = {}

        def on_delivery(error: Any, _message: Any) -> None:
            report["error"] = error
            report["done"] = True

        try:
            self._producer.produce(
                record.topic,
                key=record.aggregate_id.encode(),
                value=record.payload,
                headers=kafka_headers(record),
                on_delivery=on_delivery,
            )
        except (KafkaException, BufferError) as error:
            raise PublishError(f"produce: {error}") from error
        deadline = time.monotonic() + self._delivery_timeout + 5.0
        while not report.get("done"):
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise PublishError("delivery report timed out")
            self._producer.flush(min(remaining, 1.0))
        error = report.get("error")
        if error is not None:
            raise PublishError(f"delivery: {error}", retriable=bool(error.retriable()))

    def close(self, timeout: float) -> None:
        remaining = self._producer.flush(max(timeout, 0.0))
        if remaining:
            logger.warning("kafka producer closed with %d undelivered messages", remaining)


class RelayMetrics:
    """OTel instruments of the relay (docs/observability/metric-inventory.md#outbox).

    Prometheus names: ``nomnom_outbox_pending_messages``,
    ``nomnom_outbox_oldest_pending_age_seconds``, ``nomnom_outbox_publish_retries_total``,
    ``nomnom_outbox_publish_duration_seconds``, ``nomnom_outbox_publish_lag_seconds``.
    """

    def __init__(
        self,
        meter: Optional[metrics.Meter] = None,
        now: Callable[[], datetime] = lambda: datetime.now(timezone.utc),
    ) -> None:
        meter = meter or metrics.get_meter("nomnom.payments.outbox")
        self._now = now
        self._lock = threading.Lock()
        self._backlog: list[TopicBacklog] = []
        meter.create_observable_gauge(
            "nomnom.outbox.pending.messages",
            callbacks=[self._observe_pending],
            unit="{message}",
            description="Unpublished Outbox records.",
        )
        meter.create_observable_gauge(
            "nomnom.outbox.oldest_pending.age",
            callbacks=[self._observe_oldest_age],
            unit="s",
            description="Age of the oldest unpublished Outbox record.",
        )
        self._retries = meter.create_counter(
            "nomnom.outbox.publish.retries",
            unit="{retry}",
            description="Failed publish attempts of Outbox records.",
        )
        self._duration = meter.create_histogram(
            "nomnom.outbox.publish.duration",
            unit="s",
            description="Produce until broker delivery report of one Outbox record.",
        )
        self._lag = meter.create_histogram(
            "nomnom.outbox.publish.lag",
            unit="s",
            description="occurred_at until broker confirmation of an Outbox record.",
        )

    def set_backlog(self, backlog: Sequence[TopicBacklog]) -> None:
        with self._lock:
            self._backlog = list(backlog)

    def backlog(self) -> list[TopicBacklog]:
        with self._lock:
            return list(self._backlog)

    def published(self, record: OutboxRecord, duration: float, confirmed_at: datetime) -> None:
        attributes = {"stream": record.topic, "event_type": record.event_type}
        self._duration.record(duration, attributes)
        self._lag.record(max(0.0, (confirmed_at - record.occurred_at).total_seconds()), attributes)

    def failed(self, record: OutboxRecord, error_class: str) -> None:
        self._retries.add(1, {"stream": record.topic, "error_class": error_class})

    def _observe_pending(self, _options: metrics.CallbackOptions) -> list[metrics.Observation]:
        return [metrics.Observation(b.pending, {"stream": b.topic}) for b in self.backlog()]

    def _observe_oldest_age(self, _options: metrics.CallbackOptions) -> list[metrics.Observation]:
        now = self._now()
        return [
            metrics.Observation(
                max(0.0, (now - b.oldest_occurred_at).total_seconds()), {"stream": b.topic}
            )
            for b in self.backlog()
            if b.oldest_occurred_at is not None
        ]


@dataclass(frozen=True)
class CycleResult:
    active: bool
    published: int = 0
    failed: bool = False
    deleted: int = 0


class OutboxRelay:
    """One relay cycle; owned by ``RelayWorker``, driven directly in tests."""

    def __init__(
        self,
        store: OutboxStore,
        publisher: Publisher,
        *,
        batch_size: int,
        retention: timedelta,
        metrics_: Optional[RelayMetrics] = None,
        now: Callable[[], datetime] = lambda: datetime.now(timezone.utc),
        cleanup_interval: timedelta = CLEANUP_INTERVAL,
    ) -> None:
        self._store = store
        self._publisher = publisher
        self._batch_size = batch_size
        self._retention = retention
        self._metrics = metrics_ or RelayMetrics(now=now)
        self._now = now
        self._cleanup_interval = cleanup_interval
        self._last_cleanup: Optional[datetime] = None

    def run_once(self, should_stop: Callable[[], bool] = lambda: False) -> CycleResult:
        if not self._store.try_lock():
            # Standby: the active replica reports the backlog.
            self._metrics.set_backlog([])
            return CycleResult(active=False)
        published = 0
        failed = False
        for record in self._store.fetch_pending(self._batch_size):
            if should_stop():
                break
            started = time.perf_counter()
            try:
                self._publisher.publish(record)
            except PublishError as error:
                self._store.record_failure(record.seq, str(error))
                self._metrics.failed(record, "transient" if error.retriable else "permanent")
                logger.warning(
                    "outbox record %s (%s) not published, batch stopped: %s",
                    record.event_id,
                    record.event_type,
                    error,
                )
                failed = True
                break
            self._store.mark_published(record.seq)
            self._metrics.published(record, time.perf_counter() - started, self._now())
            published += 1
        deleted = self._cleanup()
        self._metrics.set_backlog(self._store.backlog())
        return CycleResult(active=True, published=published, failed=failed, deleted=deleted)

    def _cleanup(self) -> int:
        now = self._now()
        if self._last_cleanup is not None and now - self._last_cleanup < self._cleanup_interval:
            return 0
        self._last_cleanup = now
        deleted = self._store.delete_published_before(now - self._retention)
        if deleted:
            logger.info("outbox retention removed %d published records", deleted)
        return deleted


class RelayWorker:
    """``lifecycle.Worker`` of ``payments-relay``: polls until shutdown."""

    def __init__(
        self,
        relay: OutboxRelay,
        store: OutboxStore,
        publisher: Publisher,
        *,
        batch_size: int,
        poll_interval: float,
    ) -> None:
        self._relay = relay
        self._store = store
        self._publisher = publisher
        self._batch_size = batch_size
        self._poll_interval = poll_interval
        self._stop = threading.Event()
        self._stopped = threading.Event()

    def start(self) -> None:
        pass

    def run(self) -> None:
        try:
            while not self._stop.is_set():
                busy = False
                try:
                    result = self._relay.run_once(self._stop.is_set)
                    # A full successful batch: more records are probably waiting.
                    busy = (
                        result.active and not result.failed and result.published >= self._batch_size
                    )
                except Exception:
                    logger.exception("outbox relay cycle failed")
                if not busy:
                    self._stop.wait(self._poll_interval)
        finally:
            self._stopped.set()

    def shutdown(self, deadline: float) -> None:
        self._stop.set()
        stopped = self._stopped.wait(max(0.0, deadline - time.monotonic()))
        try:
            self._publisher.close(max(0.0, deadline - time.monotonic()))
        finally:
            # A cycle still running owns the lock session; process exit releases it.
            if stopped:
                self._store.unlock()
        if not stopped:
            raise TimeoutError("outbox relay cycle did not finish before the drain deadline")
