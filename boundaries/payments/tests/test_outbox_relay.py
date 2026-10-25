"""Outbox relay logic of ADR 31: headers, head-of-line, retention, single active instance."""

import unittest
from datetime import datetime, timedelta, timezone
from typing import Optional

from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import InMemoryMetricReader

from infrastructure.messaging import OutboxRecord, OutboxRelay, kafka_headers
from infrastructure.messaging.relay import (
    PublishError,
    RelayMetrics,
    RelayWorker,
    TopicBacklog,
    format_rfc3339_nano,
)
from pkg.config import KafkaSettings, OutboxSettings

NOW = datetime(2026, 10, 25, 12, 0, 0, tzinfo=timezone.utc)
TOPIC = "payments.v1.payment-events"


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
    def __init__(self, records: list[OutboxRecord], published_at: Optional[dict] = None) -> None:
        self.records = {r.seq: r for r in records}
        self.published: dict[int, datetime] = dict(published_at or {})
        self.failures: dict[int, list[str]] = {}
        self.locked = True
        self.cutoffs: list[datetime] = []
        self.unlocked = False

    def try_lock(self) -> bool:
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
    def __init__(self, fail_seqs: Optional[set[int]] = None, retriable: bool = True) -> None:
        self.fail_seqs = set(fail_seqs or ())
        self.retriable = retriable
        self.sent: list[int] = []

    def publish(self, record: OutboxRecord) -> None:
        if record.seq in self.fail_seqs:
            raise PublishError("broker down", retriable=self.retriable)
        self.sent.append(record.seq)

    def close(self, timeout: float) -> None:
        pass


def _metrics() -> tuple[RelayMetrics, InMemoryMetricReader]:
    reader = InMemoryMetricReader()
    provider = MeterProvider(metric_readers=[reader])
    return RelayMetrics(provider.get_meter("test"), now=lambda: NOW), reader


def _points(reader: InMemoryMetricReader) -> dict[str, list]:
    data = reader.get_metrics_data()
    result: dict[str, list] = {}
    if data is None:
        return result
    for rm in data.resource_metrics:
        for sm in rm.scope_metrics:
            for metric in sm.metrics:
                result[metric.name] = list(metric.data.data_points)
    return result


class HeadersTest(unittest.TestCase):
    def test_envelope_headers_match_orders_format(self) -> None:
        record = _record(7, datetime(2026, 10, 25, 14, 30, 1, 120000, tzinfo=timezone.utc))
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
            format_rfc3339_nano(datetime(2026, 1, 2, 3, 4, 5, 1, tzinfo=timezone.utc)),
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
        self.assertEqual(dict(retries[0].attributes), {"stream": TOPIC, "error_class": "transient"})

    def test_non_retriable_failure_is_permanent_class(self) -> None:
        store = FakeStore([_record(1)])
        self._relay(store, FakePublisher(fail_seqs={1}, retriable=False)).run_once()
        retries = _points(self.reader)["nomnom.outbox.publish.retries"]
        self.assertEqual(retries[0].attributes["error_class"], "permanent")

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
            {"stream": TOPIC, "event_type": "nomnom.payments.v1.PaymentAuthorized"},
        )
        self.assertEqual(points["nomnom.outbox.publish.duration"][0].count, 1)


class RelayWorkerTest(unittest.TestCase):
    def test_shutdown_stops_loop_and_releases_lock(self) -> None:
        import threading
        import time

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
