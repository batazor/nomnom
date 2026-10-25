"""Outbox → payments-relay → real Kafka (ADR 31).

Skipped unless ``DATABASE_URL`` (see test_postgres_integration) and, for the Kafka part,
``KAFKA_BROKERS`` are set, e.g. ``KAFKA_BROKERS=localhost:59092`` against
``docker run --rm -p 59092:9092 apache/kafka:4.1.0`` (single-node KRaft, topic
auto-creation on). Tables are truncated before every test.
"""

import os
import time
import unittest
import uuid
from datetime import timedelta
from typing import Any, override

from payment_fakes import NOW, ORCHESTRATOR, FakePaymentProvider, FakePermissions

from infrastructure.messaging import (
    KafkaPublisher,
    OutboxRelay,
    PaymentEventsMapper,
    PostgresOutboxStore,
)
from infrastructure.messaging.events import events_pb2
from infrastructure.postgres import (
    PAYMENT_EVENTS_TOPIC,
    PostgresDatabase,
    PostgresPaymentOperationRepository,
    PostgresPaymentOutbox,
    PostgresPaymentRepository,
    PostgresUnitOfWork,
    create_database,
    migrate,
)
from pkg.config import DatabaseSettings
from slices.payment.applications.authorize_payment import (
    AuthorizePaymentHandler,
    AuthorizePaymentInput,
)
from slices.payment.applications.operation_flow import PaymentOperationExecutor
from slices.payment.domains.payment import Money, OperationId, OrderId, Payment, PaymentId

DSN = os.environ.get("DATABASE_URL", "").strip()
BROKERS = os.environ.get("KAFKA_BROKERS", "").strip()


def _headers(message: Any) -> dict[str, str]:
    result: dict[str, str] = {}
    for name, value in message.headers() or []:
        result[name] = value.decode() if isinstance(value, bytes) else str(value or "")
    return result


class _Clock:
    def now(self):  # type: ignore[no-untyped-def]
        return NOW


class _Ids:
    def new_payment_id(self) -> PaymentId:
        return PaymentId(f"payment-{uuid.uuid4()}")

    def new_operation_id(self) -> OperationId:
        return OperationId(f"operation-{uuid.uuid4()}")


class _DatabaseCase(unittest.TestCase):
    database: PostgresDatabase

    @override
    @classmethod
    def setUpClass(cls) -> None:
        migrate(DSN)
        cls.database = create_database(DatabaseSettings(DATABASE__URL=DSN, DATABASE__MAX_CONNS=4))
        cls.database.check()

    @override
    @classmethod
    def tearDownClass(cls) -> None:
        cls.database.close()

    @override
    def setUp(self) -> None:
        with self.database.connection() as conn:
            conn.execute("TRUNCATE payments, payment_operations, outbox, inbox")


@unittest.skipUnless(DSN, "DATABASE_URL is not set")
class RelayLockIntegrationTest(_DatabaseCase):
    def test_single_active_instance(self) -> None:
        first = PostgresOutboxStore(self.database, DSN)
        second = PostgresOutboxStore(self.database, DSN)
        try:
            self.assertTrue(first.try_lock())
            self.assertTrue(first.try_lock())
            self.assertFalse(second.try_lock())
            first.unlock()
            self.assertTrue(second.try_lock())
        finally:
            first.unlock()
            second.unlock()


@unittest.skipUnless(DSN and BROKERS, "DATABASE_URL or KAFKA_BROKERS is not set")
class KafkaRelayIntegrationTest(_DatabaseCase):
    def _authorize(self) -> Payment:
        payments = PostgresPaymentRepository(self.database)
        order = OrderId(f"order-{uuid.uuid4()}")
        payment = Payment(PaymentId(f"payment-{uuid.uuid4()}"), order, Money.parse("25.50", "EUR"))
        payments.add(payment)
        executor = PaymentOperationExecutor(
            uow=PostgresUnitOfWork(self.database),
            payments=payments,
            operations=PostgresPaymentOperationRepository(self.database),
            outbox=PostgresPaymentOutbox(self.database, PaymentEventsMapper()),
            provider=FakePaymentProvider(),
            permissions=FakePermissions(),
            clock=_Clock(),
            ids=_Ids(),
        )
        AuthorizePaymentHandler(executor).handle(
            AuthorizePaymentInput(
                subject=ORCHESTRATOR, payment_id=payment.id, idempotency_key="authorize-1"
            )
        )
        return payment

    def test_payment_event_reaches_kafka_with_envelope_headers(self) -> None:
        from confluent_kafka import Consumer

        payment = self._authorize()
        with self.database.connection() as conn:
            row = conn.execute(
                "SELECT event_id, occurred_at FROM outbox WHERE aggregate_id = %s",
                (str(payment.id),),
            ).fetchone()
        assert row is not None
        event_id = str(row[0])

        store = PostgresOutboxStore(self.database, DSN)
        publisher = KafkaPublisher(BROKERS, "payments-relay-test", delivery_timeout=20.0)
        relay = OutboxRelay(store, publisher, batch_size=10, retention=timedelta(days=7))
        try:
            result = relay.run_once()
        finally:
            publisher.close(5)
            store.unlock()
        self.assertTrue(result.active)
        self.assertEqual((result.published, result.failed), (1, False))
        with self.database.connection() as conn:
            published = conn.execute(
                "SELECT published_at, attempts FROM outbox WHERE event_id = %s", (event_id,)
            ).fetchone()
        assert published is not None
        self.assertIsNotNone(published[0])
        self.assertEqual(published[1], 0)

        consumer = Consumer(
            {
                "bootstrap.servers": BROKERS,
                "group.id": f"payments-relay-test-{uuid.uuid4()}",
                "auto.offset.reset": "earliest",
                "enable.auto.commit": False,
            }
        )
        consumer.subscribe([PAYMENT_EVENTS_TOPIC])
        found = None
        deadline = time.monotonic() + 30
        try:
            while found is None and time.monotonic() < deadline:
                message = consumer.poll(1.0)
                if message is None or message.error() is not None:
                    continue
                if _headers(message).get("event_id") == event_id:
                    found = message
        finally:
            consumer.close()
        assert found is not None, "event not consumed from Kafka"

        headers = _headers(found)
        self.assertEqual(found.key(), str(payment.id).encode())
        self.assertEqual(headers["event_id"], event_id)
        self.assertEqual(headers["_watermill_message_uuid"], event_id)
        self.assertEqual(headers["event_type"], "nomnom.payments.v1.PaymentAuthorized")
        self.assertEqual(headers["aggregate_type"], "Payment")
        self.assertEqual(headers["aggregate_id"], str(payment.id))
        self.assertEqual(headers["aggregate_version"], "2")
        self.assertRegex(headers["occurred_at"], r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?Z$")
        event = events_pb2.PaymentAuthorized.FromString(found.value())
        self.assertEqual(
            (event.payment_id, event.order_id, event.amount.amount, event.amount.currency),
            (str(payment.id), str(payment.order_id), "25.50", "EUR"),
        )


if __name__ == "__main__":
    unittest.main()
