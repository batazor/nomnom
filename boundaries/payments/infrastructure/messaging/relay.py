"""Polling relay of the payments Outbox (ADR 31, observability of ADR 13).

One active instance holds a session-level ``pg_try_advisory_lock``; standby replicas
poll the lock only. Each cycle reads a batch of unpublished records in ``seq`` order,
publishes them one by one with an idempotent Kafka producer (``acks=all``) and marks
``published_at`` only after the broker's delivery report. The first failure stops the
batch (head-of-line): it gets ``attempts``/``last_error`` and the next cycle starts
from it again. Published records older than the retention are deleted.
"""

import logging
import threading
import time
from collections.abc import Callable
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta

from .kafka_publisher import DEFAULT_DELIVERY_TIMEOUT, KafkaPublisher
from .outbox import (
    WATERMILL_UUID_HEADER,
    OutboxBacklog,
    OutboxLease,
    OutboxQueue,
    OutboxRecord,
    OutboxRetention,
    OutboxStore,
    Publisher,
    PublishError,
    TopicBacklog,
    format_rfc3339_nano,
    kafka_headers,
)
from .postgres_store import ADVISORY_LOCK_KEY, PostgresOutboxStore
from .relay_metrics import RelayMetrics

__all__ = [
    "ADVISORY_LOCK_KEY",
    "CLEANUP_INTERVAL",
    "DEFAULT_DELIVERY_TIMEOUT",
    "WATERMILL_UUID_HEADER",
    "CycleResult",
    "KafkaPublisher",
    "OutboxBacklog",
    "OutboxLease",
    "OutboxQueue",
    "OutboxRecord",
    "OutboxRelay",
    "OutboxRetention",
    "OutboxStore",
    "PostgresOutboxStore",
    "PublishError",
    "Publisher",
    "RelayMetrics",
    "RelayWorker",
    "TopicBacklog",
    "format_rfc3339_nano",
    "kafka_headers",
]

logger = logging.getLogger(__name__)

# Retention cleanup runs at most this often, not every poll.
CLEANUP_INTERVAL = timedelta(minutes=1)


@dataclass(frozen=True)
class CycleResult:
    """Outcome of one relay cycle; ``active`` is ``False`` on a standby replica."""

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
        metrics_: RelayMetrics | None = None,
        now: Callable[[], datetime] = lambda: datetime.now(UTC),
        cleanup_interval: timedelta = CLEANUP_INTERVAL,
    ) -> None:
        self._store = store
        self._publisher = publisher
        self._batch_size = batch_size
        self._retention = retention
        self._metrics = metrics_ or RelayMetrics(now=now)
        self._now = now
        self._cleanup_interval = cleanup_interval
        self._last_cleanup: datetime | None = None

    def run_once(self, should_stop: Callable[[], bool] = lambda: False) -> CycleResult:
        """Publish one batch in ``seq`` order, then run retention cleanup and refresh metrics.

        The batch stops at the first failed record, so records of an aggregate are never
        reordered; ``should_stop`` is checked between records.
        """
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
        lease: OutboxLease,
        publisher: Publisher,
        *,
        batch_size: int,
        poll_interval: float,
    ) -> None:
        self._relay = relay
        self._lease = lease
        self._publisher = publisher
        self._batch_size = batch_size
        self._poll_interval = poll_interval
        self._stop = threading.Event()
        self._stopped = threading.Event()

    def start(self) -> None:
        """Nothing to start: the loop runs in ``run``."""

    def run(self) -> None:
        """Run relay cycles until shutdown; a failed cycle is logged and retried later."""
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
        """Stop the loop, close the publisher and release the lock before ``deadline`` (monotonic).

        Raises:
            TimeoutError: the running cycle did not finish in time; process exit
                releases the lock.
        """
        self._stop.set()
        stopped = self._stopped.wait(max(0.0, deadline - time.monotonic()))
        try:
            self._publisher.close(max(0.0, deadline - time.monotonic()))
        finally:
            # A cycle still running owns the lock session; process exit releases it.
            if stopped:
                self._lease.unlock()
        if not stopped:
            raise TimeoutError("outbox relay cycle did not finish before the drain deadline")
