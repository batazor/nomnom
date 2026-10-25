"""Outbox relay logic of ADR 31: headers, head-of-line, retention, single active instance."""

import socket
import threading
import time
import unittest
from collections.abc import Callable
from datetime import UTC, datetime, timedelta, timezone
from typing import Any, ClassVar
from unittest import mock

import psycopg
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import InMemoryMetricReader

from infrastructure.messaging import OutboxRecord, OutboxRelay, PostgresOutboxStore, kafka_headers
from infrastructure.messaging.postgres_store import ADVISORY_LOCK_KEY, advisory_lock_key_parts
from infrastructure.messaging.relay import (
    FatalPublishError,
    PublishError,
    PublishInterruptedError,
    RelayMetrics,
    RelayWorker,
    TopicBacklog,
    format_rfc3339_nano,
)
from pkg.config import KafkaSettings, OutboxSettings

NOW = datetime(2026, 10, 25, 12, 0, 0, tzinfo=UTC)
TOPIC = "payments.v1.payment-events"
_TOPIC_ATTR = {"messaging.destination.name": TOPIC}


def _record(seq: int, occurred_at: datetime = NOW - timedelta(seconds=2)) -> OutboxRecord:
    return OutboxRecord(
        seq=seq,
        event_id=f"00000000-0000-5000-8000-{seq:012d}",
        event_type="nomnom.payments.v1.PaymentAuthorized",
        aggregate_type="Payment",
        aggregate_id=f"payment-{seq}",
        aggregate_version=seq,
        occurred_at=occurred_at,
        topic=TOPIC,
        payload=b"p",
        metadata={"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"},
    )


class FakeStore:
    def __init__(
        self, records: list[OutboxRecord], published_at: dict[int, datetime] | None = None
    ) -> None:
        self.records = {r.seq: r for r in records}
        self.published: dict[int, datetime] = dict(published_at or {})
        self.failures: dict[int, list[str]] = {}
        self.locked = True
        self.cutoffs: list[datetime] = []
        self.unlocked = False
        self.lock_checks = 0
        # Lock session dies after this many successful holds_lock checks.
        self.lose_lock_after: int | None = None

    def try_lock(self) -> bool:
        return self.locked

    def holds_lock(self) -> bool:
        if self.lose_lock_after is not None and self.lock_checks >= self.lose_lock_after:
            self.locked = False
        self.lock_checks += 1
        return self.locked

    def unlock(self) -> None:
        self.unlocked = True

    def fetch_pending(self, limit: int) -> list[OutboxRecord]:
        pending = [r for s, r in sorted(self.records.items()) if s not in self.published]
        return pending[:limit]

    def mark_published(self, seq: int) -> None:
        self.published[seq] = NOW

    def record_failure(self, seq: int, error: str) -> None:
        self.failures.setdefault(seq, []).append(error)

    def delete_published_before(self, cutoff: datetime) -> int:
        self.cutoffs.append(cutoff)
        old = [s for s, at in self.published.items() if at < cutoff]
        for seq in old:
            del self.published[seq]
            del self.records[seq]
        return len(old)

    def backlog(self) -> list[TopicBacklog]:
        pending = self.fetch_pending(10_000)
        if not pending:
            return []
        return [TopicBacklog(TOPIC, len(pending), min(r.occurred_at for r in pending))]


class FakePublisher:
    def __init__(
        self,
        fail_seqs: set[int] | None = None,
        retriable: bool = True,
        fatal_seqs: set[int] | None = None,
        block_seqs: set[int] | None = None,
    ) -> None:
        self.fail_seqs = set(fail_seqs or ())
        self.retriable = retriable
        self.fatal_seqs = set(fatal_seqs or ())
        # Records whose delivery report never arrives (broker outage) until should_stop.
        self.block_seqs = set(block_seqs or ())
        self.blocked = threading.Event()
        self.sent: list[int] = []

    def publish(self, record: OutboxRecord, should_stop: Callable[[], bool]) -> None:
        if record.seq in self.fatal_seqs:
            raise FatalPublishError("delivery: fatal idempotent producer error")
        if record.seq in self.fail_seqs:
            raise PublishError("broker down", retriable=self.retriable)
        if record.seq in self.block_seqs:
            self.blocked.set()
            while not should_stop():
                time.sleep(0.01)
            raise PublishInterruptedError("interrupted")
        self.sent.append(record.seq)

    def close(self, timeout: float) -> None:
        pass


def _metrics() -> tuple[RelayMetrics, InMemoryMetricReader]:
    reader = InMemoryMetricReader()
    provider = MeterProvider(metric_readers=[reader])
    return RelayMetrics(provider.get_meter("test"), now=lambda: NOW), reader


def _points(reader: InMemoryMetricReader) -> dict[str, list[Any]]:
    data = reader.get_metrics_data()
    result: dict[str, list[Any]] = {}
    if data is None:
        return result
    for rm in data.resource_metrics:
        for sm in rm.scope_metrics:
            for metric in sm.metrics:
                result[metric.name] = list(metric.data.data_points)
    return result


class HeadersTest(unittest.TestCase):
    def test_envelope_headers_match_orders_format(self) -> None:
        record = _record(7, datetime(2026, 10, 25, 14, 30, 1, 120000, tzinfo=UTC))
        headers = dict(kafka_headers(record))
        self.assertEqual(
            headers,
            {
                "event_id": record.event_id.encode(),
                "_watermill_message_uuid": record.event_id.encode(),
                "event_type": b"nomnom.payments.v1.PaymentAuthorized",
                "occurred_at": b"2026-10-25T14:30:01.12Z",
                "aggregate_type": b"Payment",
                "aggregate_id": b"payment-7",
                "aggregate_version": b"7",
                "traceparent": b"00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
            },
        )

    def test_no_traceparent_header_without_trace(self) -> None:
        record = OutboxRecord(1, "id", "t", "Payment", "p", 1, NOW, TOPIC, b"")
        self.assertNotIn("traceparent", dict(kafka_headers(record)))

    def test_rfc3339_nano_is_utc_and_trims_zeros(self) -> None:
        plus_three = timezone(timedelta(hours=3))
        self.assertEqual(
            format_rfc3339_nano(datetime(2026, 1, 2, 6, 4, 5, tzinfo=plus_three)),
            "2026-01-02T03:04:05Z",
        )
        self.assertEqual(
            format_rfc3339_nano(datetime(2026, 1, 2, 3, 4, 5, 1, tzinfo=UTC)),
            "2026-01-02T03:04:05.000001Z",
        )


class RelayCycleTest(unittest.TestCase):
    def _relay(self, store: FakeStore, publisher: FakePublisher, **kwargs) -> OutboxRelay:
        metrics, self.reader = _metrics()
        return OutboxRelay(
            store,
            publisher,
            batch_size=kwargs.pop("batch_size", 10),
            retention=timedelta(days=7),
            metrics_=metrics,
            now=lambda: NOW,
            **kwargs,
        )

    def test_publishes_batch_in_seq_order_and_marks_published(self) -> None:
        store = FakeStore([_record(3), _record(1), _record(2)])
        publisher = FakePublisher()
        result = self._relay(store, publisher).run_once()
        self.assertEqual(publisher.sent, [1, 2, 3])
        self.assertEqual(set(store.published), {1, 2, 3})
        self.assertEqual((result.published, result.failed), (3, False))

    def test_batch_size_limits_one_cycle(self) -> None:
        store = FakeStore([_record(1), _record(2), _record(3)])
        publisher = FakePublisher()
        self._relay(store, publisher, batch_size=2).run_once()
        self.assertEqual(publisher.sent, [1, 2])

    def test_first_failure_stops_batch_head_of_line(self) -> None:
        store = FakeStore([_record(1), _record(2), _record(3)])
        publisher = FakePublisher(fail_seqs={2})
        relay = self._relay(store, publisher)
        result = relay.run_once()
        self.assertEqual(publisher.sent, [1])
        self.assertEqual(set(store.published), {1})
        self.assertEqual(store.failures, {2: ["broker down"]})
        self.assertTrue(result.failed)

        # Next cycle starts from the failed record again.
        relay.run_once()
        self.assertEqual(publisher.sent, [1])
        self.assertEqual(store.failures, {2: ["broker down", "broker down"]})

        publisher.fail_seqs.clear()
        relay.run_once()
        self.assertEqual(publisher.sent, [1, 2, 3])

        points = _points(self.reader)
        retries = points["nomnom.outbox.publish.retries"]
        self.assertEqual(retries[0].value, 2)
        self.assertEqual(
            dict(retries[0].attributes),
            {
                "messaging.destination.name": TOPIC,
                "nomnom.event.type": "nomnom.payments.v1.PaymentAuthorized",
                "nomnom.error.class": "transient",
            },
        )

    def test_non_retriable_failure_is_permanent_class(self) -> None:
        store = FakeStore([_record(1)])
        self._relay(store, FakePublisher(fail_seqs={1}, retriable=False)).run_once()
        retries = _points(self.reader)["nomnom.outbox.publish.retries"]
        self.assertEqual(retries[0].attributes["nomnom.error.class"], "permanent")

    def test_standby_without_lock_reads_nothing(self) -> None:
        store = FakeStore([_record(1)])
        store.locked = False
        publisher = FakePublisher()
        result = self._relay(store, publisher).run_once()
        self.assertFalse(result.active)
        self.assertEqual(publisher.sent, [])
        self.assertEqual(store.cutoffs, [])

    def test_retention_deletes_only_old_published_records(self) -> None:
        old = NOW - timedelta(days=8)
        recent = NOW - timedelta(days=1)
        store = FakeStore([_record(1), _record(2), _record(3)], published_at={1: old, 2: recent})
        publisher = FakePublisher(fail_seqs={3})
        result = self._relay(store, publisher).run_once()
        self.assertEqual(store.cutoffs, [NOW - timedelta(days=7)])
        self.assertEqual(result.deleted, 1)
        self.assertNotIn(1, store.records)
        # Recent published and unpublished (failed) records stay.
        self.assertIn(2, store.records)
        self.assertIn(3, store.records)

    def test_retention_runs_at_most_once_per_interval(self) -> None:
        store = FakeStore([])
        relay = self._relay(store, FakePublisher())
        relay.run_once()
        relay.run_once()
        self.assertEqual(len(store.cutoffs), 1)

    def test_metrics_backlog_duration_and_lag(self) -> None:
        store = FakeStore([_record(1), _record(2, NOW - timedelta(seconds=30))])
        relay = self._relay(store, FakePublisher(fail_seqs={2}))
        relay.run_once()
        points = _points(self.reader)
        self.assertEqual(points["nomnom.outbox.pending.messages"][0].value, 1)
        self.assertEqual(points["nomnom.outbox.oldest_pending.age"][0].value, 30.0)
        lag = points["nomnom.outbox.publish.lag"][0]
        self.assertEqual((lag.count, lag.sum), (1, 2.0))
        self.assertEqual(
            dict(lag.attributes),
            {
                "messaging.destination.name": TOPIC,
                "nomnom.event.type": "nomnom.payments.v1.PaymentAuthorized",
            },
        )
        duration = points["nomnom.outbox.publish.duration"][0]
        self.assertEqual(duration.count, 1)
        self.assertEqual(duration.attributes["nomnom.outcome"], "success")

    def test_lock_checked_before_every_publish(self) -> None:
        store = FakeStore([_record(1), _record(2), _record(3)])
        self._relay(store, FakePublisher()).run_once()
        self.assertEqual(store.lock_checks, 3)

    def test_lock_lost_mid_batch_stops_publishing(self) -> None:
        store = FakeStore([_record(1), _record(2), _record(3)])
        store.lose_lock_after = 2
        publisher = FakePublisher()
        result = self._relay(store, publisher).run_once()
        # The standby may already publish 3: this replica must not publish it too.
        self.assertEqual(publisher.sent, [1, 2])
        self.assertEqual(set(store.published), {1, 2})
        self.assertEqual(store.failures, {})
        self.assertEqual((result.published, result.failed, result.lock_lost), (2, False, True))
        # No retention cleanup and no backlog read without the lock: zeros only.
        self.assertEqual(store.cutoffs, [])
        pending = _points(self.reader)["nomnom.outbox.pending.messages"]
        self.assertEqual([(dict(p.attributes), p.value) for p in pending], [(_TOPIC_ATTR, 0)])

    def test_fatal_publish_error_is_recorded_and_raised(self) -> None:
        store = FakeStore([_record(1), _record(2), _record(3)])
        publisher = FakePublisher(fatal_seqs={2})
        with self.assertRaises(FatalPublishError):
            self._relay(store, publisher).run_once()
        self.assertEqual(publisher.sent, [1])
        self.assertEqual(set(store.published), {1})
        self.assertEqual(list(store.failures), [2])
        retries = _points(self.reader)["nomnom.outbox.publish.retries"]
        self.assertEqual(retries[0].attributes["nomnom.error.class"], "permanent")

    def test_interrupted_publish_leaves_record_unpublished_without_failure(self) -> None:
        store = FakeStore([_record(1), _record(2)])
        publisher = FakePublisher(block_seqs={1})
        result = self._relay(store, publisher).run_once(lambda: publisher.blocked.is_set())
        self.assertEqual(publisher.sent, [])
        self.assertEqual(store.published, {})
        self.assertEqual(store.failures, {})
        self.assertEqual((result.published, result.failed), (0, False))


class BacklogGaugesTest(unittest.TestCase):
    def _observed(self, reader: InMemoryMetricReader) -> dict[str, dict[str, float]]:
        return {
            name: {p.attributes["messaging.destination.name"]: p.value for p in points}
            for name, points in _points(reader).items()
            if name.startswith(("nomnom.outbox.pending", "nomnom.outbox.oldest_pending"))
        }

    def test_nothing_reported_before_first_backlog(self) -> None:
        _, reader = _metrics()
        self.assertEqual(self._observed(reader), {})

    def test_known_topic_reports_zero_without_backlog(self) -> None:
        metrics, reader = _metrics()
        metrics.set_backlog([])
        observed = self._observed(reader)
        self.assertEqual(observed["nomnom.outbox.pending.messages"], {TOPIC: 0})
        self.assertEqual(observed["nomnom.outbox.oldest_pending.age"], {TOPIC: 0.0})

    def test_drained_topic_reports_zero_instead_of_disappearing(self) -> None:
        metrics, reader = _metrics()
        other = "payments.v1.other"
        metrics.set_backlog(
            [
                TopicBacklog(TOPIC, 3, NOW - timedelta(seconds=40)),
                TopicBacklog(other, 1, NOW - timedelta(seconds=5)),
            ]
        )
        observed = self._observed(reader)
        self.assertEqual(observed["nomnom.outbox.pending.messages"], {TOPIC: 3, other: 1})
        self.assertEqual(observed["nomnom.outbox.oldest_pending.age"], {TOPIC: 40.0, other: 5.0})
        metrics.set_backlog([])
        observed = self._observed(reader)
        self.assertEqual(observed["nomnom.outbox.pending.messages"], {TOPIC: 0, other: 0})
        self.assertEqual(observed["nomnom.outbox.oldest_pending.age"], {TOPIC: 0.0, other: 0.0})

    def test_relay_cycle_after_drain_reports_zero(self) -> None:
        metrics, reader = _metrics()
        store = FakeStore([_record(1)])
        relay = OutboxRelay(
            store, FakePublisher(), batch_size=10, retention=timedelta(days=7), metrics_=metrics
        )
        relay.run_once()
        observed = self._observed(reader)
        self.assertEqual(observed["nomnom.outbox.pending.messages"], {TOPIC: 0})
        self.assertEqual(observed["nomnom.outbox.oldest_pending.age"], {TOPIC: 0.0})


class LockOwnershipCheckTest(unittest.TestCase):
    def test_key_split_matches_pg_locks_classid_objid(self) -> None:
        self.assertEqual(advisory_lock_key_parts(0x0000_0001_0000_0002), (1, 2))
        self.assertEqual(advisory_lock_key_parts(-1), (0xFFFF_FFFF, 0xFFFF_FFFF))
        self.assertEqual(advisory_lock_key_parts(-(2**63)), (0x8000_0000, 0))
        classid, objid = advisory_lock_key_parts(ADVISORY_LOCK_KEY)
        self.assertEqual((classid << 32) | objid, int.from_bytes(b"payments", "big"))

    def _store_with(self, conn: mock.MagicMock, timeout: float = 5.0) -> PostgresOutboxStore:
        store = PostgresOutboxStore(mock.MagicMock(), "postgresql://x", lock_query_timeout=timeout)
        with mock.patch("infrastructure.messaging.postgres_store.psycopg.connect") as connect:
            connect.return_value = conn
            self.assertTrue(store.try_lock())
        options = connect.call_args.kwargs
        self.assertEqual(options["keepalives"], 1)
        self.assertTrue(
            {"keepalives_idle", "keepalives_interval", "keepalives_count"} <= set(options)
        )
        return store

    def _conn(self, held: object) -> mock.MagicMock:
        conn = mock.MagicMock()
        conn.execute.return_value.fetchone.side_effect = [("5000ms",), (True,), held]
        return conn

    def test_lock_held_checks_pg_locks_with_key_parts(self) -> None:
        conn = self._conn((True,))
        store = self._store_with(conn)
        self.assertTrue(store.holds_lock())
        query, params = conn.execute.call_args.args
        self.assertIn("pg_locks", query)
        self.assertIn("pg_backend_pid()", query)
        self.assertEqual(params, advisory_lock_key_parts(ADVISORY_LOCK_KEY))
        conn.close.assert_not_called()

    def test_lock_missing_from_session_is_lost(self) -> None:
        conn = self._conn((False,))
        store = self._store_with(conn)
        self.assertFalse(store.holds_lock())
        conn.close.assert_called_once()
        self.assertFalse(store.holds_lock())

    def test_query_error_is_lost(self) -> None:
        conn = mock.MagicMock()
        conn.execute.return_value.fetchone.side_effect = [
            ("5000ms",),
            (True,),
            psycopg.OperationalError("terminated"),
        ]
        store = self._store_with(conn)
        self.assertFalse(store.holds_lock())
        conn.close.assert_called_once()

    def test_query_past_deadline_is_lost(self) -> None:
        # The libpq socket of a half-open connection: the reply never arrives.
        client, server = socket.socketpair()
        self.addCleanup(client.close)
        self.addCleanup(server.close)
        conn = mock.MagicMock()
        conn.pgconn.socket = client.fileno()
        replies = iter([("5000ms",), (True,)])

        def fetchone() -> object:
            reply = next(replies, None)
            if reply is not None:
                return reply
            # Blocks like libpq until the deadline shuts the socket down.
            if client.recv(1) == b"":
                raise psycopg.OperationalError("server closed the connection unexpectedly")
            return (True,)

        conn.execute.return_value.fetchone.side_effect = fetchone
        store = self._store_with(conn, timeout=0.1)
        started = time.monotonic()
        self.assertFalse(store.holds_lock())
        self.assertLess(time.monotonic() - started, 2.0)
        conn.close.assert_called_once()


class RelayWorkerTest(unittest.TestCase):
    def test_shutdown_stops_loop_and_releases_lock(self) -> None:

        store = FakeStore([_record(1)])
        publisher = FakePublisher()
        metrics, _ = _metrics()
        relay = OutboxRelay(
            store, publisher, batch_size=10, retention=timedelta(days=7), metrics_=metrics
        )
        worker = RelayWorker(relay, store, publisher, batch_size=10, poll_interval=0.01)
        worker.start()
        thread = threading.Thread(target=worker.run)
        thread.start()
        deadline = time.monotonic() + 2
        while not publisher.sent and time.monotonic() < deadline:
            time.sleep(0.01)
        worker.shutdown(time.monotonic() + 2)
        thread.join(2)
        self.assertFalse(thread.is_alive())
        self.assertEqual(publisher.sent, [1])
        self.assertTrue(store.unlocked)

    def test_shutdown_releases_lock_through_lease_only(self) -> None:
        # AC-640: the worker needs the lock role only, not the whole Outbox store.
        class Lease:
            def __init__(self) -> None:
                self.unlocked = False

            def try_lock(self) -> bool:
                return False

            def holds_lock(self) -> bool:
                return False

            def unlock(self) -> None:
                self.unlocked = True

        lease = Lease()
        publisher = FakePublisher()
        metrics, _ = _metrics()
        relay = OutboxRelay(
            FakeStore([]), publisher, batch_size=10, retention=timedelta(days=7), metrics_=metrics
        )
        worker = RelayWorker(relay, lease, publisher, batch_size=10, poll_interval=0.01)
        thread = threading.Thread(target=worker.run)
        thread.start()
        worker.shutdown(time.monotonic() + 2)
        thread.join(2)

        self.assertFalse(thread.is_alive())
        self.assertTrue(lease.unlocked)

    def _worker(
        self, store: FakeStore, publisher: FakePublisher
    ) -> tuple[RelayWorker, threading.Thread, list[BaseException]]:
        metrics, _ = _metrics()
        relay = OutboxRelay(
            store, publisher, batch_size=10, retention=timedelta(days=7), metrics_=metrics
        )
        worker = RelayWorker(relay, store, publisher, batch_size=10, poll_interval=0.01)
        errors: list[BaseException] = []

        def run() -> None:
            try:
                worker.run()
            except BaseException as error:
                errors.append(error)

        worker.start()
        thread = threading.Thread(target=run, daemon=True)
        thread.start()
        return worker, thread, errors

    def test_fatal_publish_error_stops_worker_with_error(self) -> None:
        store = FakeStore([_record(1)])
        worker, thread, errors = self._worker(store, FakePublisher(fatal_seqs={1}))
        thread.join(2)
        self.assertFalse(thread.is_alive(), "worker kept running after a fatal error")
        self.assertEqual([type(e) for e in errors], [FatalPublishError])
        # A standby takes over without waiting for the process to exit.
        self.assertTrue(store.unlocked)
        worker.shutdown(time.monotonic() + 1)

    def test_fatal_error_ends_lifecycle_with_error(self) -> None:
        from pkg.lifecycle import Lifecycle, LifecycleConfig, LifecycleError

        store = FakeStore([_record(1)])
        publisher = FakePublisher(fatal_seqs={1})
        metrics, _ = _metrics()
        relay = OutboxRelay(
            store, publisher, batch_size=10, retention=timedelta(days=7), metrics_=metrics
        )
        worker = RelayWorker(relay, store, publisher, batch_size=10, poll_interval=0.01)
        lifecycle = Lifecycle(LifecycleConfig(0, readiness_delay=0.0, drain_timeout=1.0))
        with self.assertRaisesRegex(LifecycleError, "worker stopped before shutdown"):
            lifecycle.run(worker, threading.Event())

    def test_shutdown_interrupts_blocked_publish_within_deadline(self) -> None:
        store = FakeStore([_record(1), _record(2)])
        publisher = FakePublisher(block_seqs={1})
        worker, thread, errors = self._worker(store, publisher)
        self.assertTrue(publisher.blocked.wait(2))
        started = time.monotonic()
        # Raises TimeoutError if the cycle does not finish before the deadline.
        worker.shutdown(started + 1)
        self.assertLess(time.monotonic() - started, 1)
        thread.join(1)
        self.assertFalse(thread.is_alive())
        self.assertEqual(errors, [])
        self.assertEqual((publisher.sent, store.published, store.failures), ([], {}, {}))
        self.assertTrue(store.unlocked)


class _FakeProducer:
    """``confluent_kafka.Producer`` stand-in: reports are scripted per test."""

    instances: ClassVar[list[_FakeProducer]] = []

    def __init__(self, config: dict[str, Any]) -> None:
        self.config = config
        self.produced: list[Callable[[Any, Any], None]] = []
        self.produce_error: Exception | None = None
        # Called on every flush: scripts delivery reports and error callbacks.
        self.on_flush: Callable[[_FakeProducer], None] = lambda _p: None
        self.purged = False
        self.queued = 0
        _FakeProducer.instances.append(self)

    def produce(self, topic: str, **kwargs: Any) -> None:
        if self.produce_error is not None:
            raise self.produce_error
        self.produced.append(kwargs["on_delivery"])
        self.queued += 1

    def flush(self, timeout: float) -> int:
        self.on_flush(self)
        if not self.purged:
            time.sleep(min(timeout, 0.01))
        return self.queued

    def purge(self) -> None:
        self.purged = True
        self.queued = 0

    def deliver(self, error: Any) -> None:
        self.queued = 0
        for callback in self.produced:
            callback(error, None)
        self.produced.clear()


class KafkaPublisherTest(unittest.TestCase):
    def _publisher(self, delivery_timeout: float = 5.0) -> tuple[Any, _FakeProducer]:
        from unittest import mock

        from infrastructure.messaging import KafkaPublisher

        with mock.patch("confluent_kafka.Producer", _FakeProducer):
            publisher = KafkaPublisher("broker:9092", "test", delivery_timeout=delivery_timeout)
        return publisher, _FakeProducer.instances[-1]

    def test_success_and_retriable_delivery_errors(self) -> None:
        from confluent_kafka import KafkaError

        publisher, producer = self._publisher()
        producer.on_flush = lambda p: p.deliver(None)
        publisher.publish(_record(1))
        producer.on_flush = lambda p: p.deliver(
            KafkaError(KafkaError._MSG_TIMED_OUT, retriable=True)
        )
        with self.assertRaises(PublishError) as raised:
            publisher.publish(_record(2))
        self.assertNotIsInstance(raised.exception, FatalPublishError)
        self.assertTrue(raised.exception.retriable)

    def test_fatal_delivery_error_poisons_producer(self) -> None:
        from confluent_kafka import KafkaError

        publisher, producer = self._publisher()
        producer.on_flush = lambda p: p.deliver(
            KafkaError(KafkaError.OUT_OF_ORDER_SEQUENCE_NUMBER, fatal=True)
        )
        with self.assertRaises(FatalPublishError):
            publisher.publish(_record(1))
        # Later publishes fail at once, without producing.
        with self.assertRaises(FatalPublishError):
            publisher.publish(_record(2))
        self.assertEqual(producer.produced, [])

    def test_fatal_code_in_delivery_report_is_fatal(self) -> None:
        from confluent_kafka import KafkaError

        publisher, producer = self._publisher()
        producer.on_flush = lambda p: p.deliver(KafkaError(KafkaError._FATAL))
        with self.assertRaises(FatalPublishError):
            publisher.publish(_record(1))

    def test_fatal_error_callback_ends_wait(self) -> None:
        from confluent_kafka import KafkaError

        publisher, producer = self._publisher(delivery_timeout=30.0)
        error_cb = producer.config["error_cb"]
        producer.on_flush = lambda _p: error_cb(
            KafkaError(KafkaError.CLUSTER_AUTHORIZATION_FAILED, fatal=True)
        )
        started = time.monotonic()
        with self.assertRaises(FatalPublishError):
            publisher.publish(_record(1))
        self.assertLess(time.monotonic() - started, 1)

    def test_non_fatal_error_callback_keeps_waiting(self) -> None:
        from confluent_kafka import KafkaError

        publisher, producer = self._publisher()
        error_cb = producer.config["error_cb"]

        def flush(p: _FakeProducer) -> None:
            error_cb(KafkaError(KafkaError._ALL_BROKERS_DOWN))
            p.deliver(None)

        producer.on_flush = flush
        publisher.publish(_record(1))

    def test_fatal_produce_exception(self) -> None:
        from confluent_kafka import KafkaError, KafkaException

        publisher, producer = self._publisher()
        producer.produce_error = KafkaException(KafkaError(KafkaError._FATAL, fatal=True))
        with self.assertRaises(FatalPublishError):
            publisher.publish(_record(1))
        producer.produce_error = BufferError("queue full")
        with self.assertRaises(FatalPublishError):
            publisher.publish(_record(2))

    def test_non_fatal_produce_errors(self) -> None:
        from confluent_kafka import KafkaError, KafkaException

        publisher, producer = self._publisher()
        for error in (
            BufferError("queue full"),
            KafkaException(KafkaError(KafkaError._UNKNOWN_TOPIC)),
        ):
            producer.produce_error = error
            with self.assertRaises(PublishError) as raised:
                publisher.publish(_record(1))
            self.assertNotIsInstance(raised.exception, FatalPublishError)

    def test_should_stop_interrupts_delivery_wait(self) -> None:
        publisher, _producer = self._publisher(delivery_timeout=30.0)
        stop_at = time.monotonic() + 0.2
        started = time.monotonic()
        with self.assertRaises(PublishInterruptedError):
            publisher.publish(_record(1), lambda: time.monotonic() >= stop_at)
        self.assertLess(time.monotonic() - started, 1)

    def test_close_purges_undelivered_messages(self) -> None:
        publisher, producer = self._publisher()
        with self.assertRaises(PublishInterruptedError):
            publisher.publish(_record(1), lambda: True)
        publisher.close(0.05)
        self.assertTrue(producer.purged)
        self.assertEqual(producer.queued, 0)

    def test_close_without_leftovers_does_not_purge(self) -> None:
        publisher, producer = self._publisher()
        publisher.close(0.05)
        self.assertFalse(producer.purged)


class RelayConfigTest(unittest.TestCase):
    def test_brokers_required(self) -> None:
        with self.assertRaisesRegex(ValueError, "KAFKA__BROKERS"):
            KafkaSettings(KAFKA__BROKERS="  ")

    def test_brokers_normalized(self) -> None:
        settings = KafkaSettings(KAFKA__BROKERS=" a:9092, b:9092 ,")
        self.assertEqual(settings.brokers, "a:9092,b:9092")
        self.assertEqual(settings.client_id, "payments-relay")

    def test_outbox_defaults_and_durations(self) -> None:
        defaults = OutboxSettings()
        self.assertEqual(defaults.retention, timedelta(days=7))
        self.assertEqual(defaults.batch_size, 100)
        self.assertEqual(defaults.poll_interval, timedelta(seconds=1))
        custom = OutboxSettings(
            OUTBOX__POLL_INTERVAL="250ms", OUTBOX__BATCH_SIZE="5", OUTBOX__RETENTION="60m"
        )
        self.assertEqual(
            (custom.poll_interval, custom.batch_size, custom.retention),
            (timedelta(milliseconds=250), 5, timedelta(hours=1)),
        )
        with self.assertRaises(ValueError):
            OutboxSettings(OUTBOX__RETENTION="0s")


if __name__ == "__main__":
    unittest.main()
