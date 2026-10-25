"""Integration with a real PostgreSQL: migrations, repositories, Unit of Work, Outbox, Inbox.

Skipped unless ``DATABASE_URL`` is set, e.g.
``DATABASE_URL=postgresql://payments:payments@localhost:55432/payments`` against
``docker run --rm -p 55432:5432 -e POSTGRES_USER=payments -e POSTGRES_PASSWORD=payments
postgres:18.6-alpine3.24``. Tables are truncated before every test.
"""

import contextlib
import os
import unittest
import uuid
from datetime import timedelta
from typing import Any, override

import psycopg
from payment_fakes import NOW, ORCHESTRATOR, FakePaymentProvider, FakePermissions

from infrastructure.postgres import (
    PAYMENT_EVENTS_TOPIC,
    NoPublishedContracts,
    OutboxMessage,
    PostgresDatabase,
    PostgresPaymentOperationRepository,
    PostgresPaymentOutbox,
    PostgresPaymentRepository,
    PostgresProviderMessageInbox,
    PostgresUnitOfWork,
    create_database,
    migrate,
    outbox_event_id,
)
from pkg.application import AggregateRef, ConcurrencyConflictError
from pkg.config import DatabaseSettings
from slices.payment.applications.authorize_payment import (
    AuthorizePaymentHandler,
    AuthorizePaymentInput,
)
from slices.payment.applications.operation_flow import PaymentOperationExecutor
from slices.payment.domains.payment import (
    DuplicateIdempotencyKeyError,
    DuplicatePaymentError,
    Money,
    OperationAlreadyPendingError,
    OperationId,
    OperationKind,
    OperationState,
    OrderId,
    Payment,
    PaymentAuthorized,
    PaymentDomainEvent,
    PaymentId,
    PaymentOperation,
    PaymentState,
)

AUTHORIZED_TYPE = "nomnom.payments.v1.PaymentAuthorized"


class _AuthorizedOnly:
    """Test mapper: only PaymentAuthorized has a contract; payload is a stand-in."""

    def map(self, aggregate: AggregateRef, event: PaymentDomainEvent) -> OutboxMessage | None:
        if not isinstance(event, PaymentAuthorized):
            return None
        return OutboxMessage(
            AUTHORIZED_TYPE, PAYMENT_EVENTS_TOPIC, f"authorized:{event.payment_id}".encode()
        )


DSN = os.environ.get("DATABASE_URL", "").strip()
AMOUNT = Money.parse("25.00", "EUR")


class _Clock:
    def now(self):  # type: ignore[no-untyped-def]
        return NOW


class _Ids:
    def __init__(self) -> None:
        self.count = 0

    def new_payment_id(self) -> PaymentId:
        self.count += 1
        return PaymentId(f"payment-{self.count}")

    def new_operation_id(self) -> OperationId:
        self.count += 1
        return OperationId(f"operation-{self.count}")


@unittest.skipUnless(DSN, "DATABASE_URL is not set")
class PostgresIntegrationTest(unittest.TestCase):
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
        self.uow = PostgresUnitOfWork(self.database)
        self.payments = PostgresPaymentRepository(self.database)
        self.operations = PostgresPaymentOperationRepository(self.database)
        self.outbox = PostgresPaymentOutbox(self.database, _AuthorizedOnly())
        self.inbox = PostgresProviderMessageInbox(self.database)

    def _payment(self, payment_id: str = "payment-1", order_id: str = "order-1") -> Payment:
        payment = Payment(PaymentId(payment_id), OrderId(order_id), AMOUNT)
        self.payments.add(payment)
        stored = self.payments.get(payment.id)
        assert stored is not None
        return stored

    def _operation(
        self, operation_id: str, key: str, payment_id: str = "payment-1", minutes: int = 0
    ) -> PaymentOperation:
        return PaymentOperation(
            OperationId(operation_id),
            PaymentId(payment_id),
            OperationKind.AUTHORIZE,
            key,
            AMOUNT,
            NOW + timedelta(minutes=minutes),
        )

    def _outbox_rows(self) -> list[tuple[Any, ...]]:
        with self.database.connection() as conn:
            return conn.execute(
                "SELECT event_id, aggregate_type, aggregate_id, aggregate_version, event_type, "
                "topic, payload, attempts, published_at FROM outbox ORDER BY seq"
            ).fetchall()

    def test_migrations_are_idempotent(self) -> None:
        self.assertEqual(migrate(DSN), [])

    def test_payment_round_trip_keeps_exact_money(self) -> None:
        payment = self._payment()
        self.assertEqual(payment.version, 1)
        self.assertEqual(payment.amount, AMOUNT)
        self.assertEqual(str(payment.amount.amount), "25.00")
        self.assertEqual(payment.state, PaymentState.PENDING)
        by_order = self.payments.get_by_order(OrderId("order-1"))
        self.assertIsNotNone(by_order)
        self.assertIsNone(self.payments.get(PaymentId("missing")))

    def test_second_payment_for_order_is_duplicate(self) -> None:
        self._payment()
        with self.assertRaises(DuplicatePaymentError):
            self.payments.add(Payment(PaymentId("payment-2"), OrderId("order-1"), AMOUNT))

    def test_save_with_stale_version_is_concurrency_conflict(self) -> None:
        first = self._payment()
        second = self.payments.get(first.id)
        assert second is not None
        first.authorize("auth-ref")
        self.assertEqual(self.payments.save(first), 2)
        second.authorize("other-ref")
        with self.assertRaises(ConcurrencyConflictError):
            self.payments.save(second)
        stored = self.payments.get(first.id)
        assert stored is not None
        self.assertEqual(
            (stored.state, stored.provider_reference), (PaymentState.AUTHORIZED, "auth-ref")
        )

    def test_operation_uniqueness_maps_to_domain_errors(self) -> None:
        self._payment()
        self._payment("payment-2", "order-2")
        self.operations.add(self._operation("operation-1", "key-1"))
        with self.assertRaises(DuplicateIdempotencyKeyError):
            self.operations.add(self._operation("operation-2", "key-1", "payment-2"))
        with self.assertRaises(OperationAlreadyPendingError):
            self.operations.add(self._operation("operation-3", "key-3"))

    def test_completed_operation_frees_pending_slot_and_history_is_ordered(self) -> None:
        self._payment()
        first = self._operation("operation-1", "key-1", minutes=1)
        self.operations.add(first)
        loaded = self.operations.find_pending(PaymentId("payment-1"))
        assert loaded is not None
        loaded.decline("insufficient funds", NOW + timedelta(minutes=2))
        self.assertEqual(self.operations.save(loaded), 2)
        with self.assertRaises(ConcurrencyConflictError):
            self.operations.save(loaded)
        self.operations.add(self._operation("operation-0", "key-0", minutes=0))

        history = self.operations.list_for_payment(PaymentId("payment-1"))
        self.assertEqual([op.id for op in history], ["operation-0", "operation-1"])
        declined = self.operations.get_by_idempotency_key("key-1")
        assert declined is not None
        self.assertEqual(declined.state, OperationState.DECLINED)
        self.assertEqual(declined.decline_reason, "insufficient funds")
        self.assertEqual(declined.completed_at, NOW + timedelta(minutes=2))

    def test_unit_of_work_rolls_back_every_repository(self) -> None:
        def work() -> None:
            payment = Payment(PaymentId("payment-1"), OrderId("order-1"), AMOUNT)
            self.payments.add(payment)
            self.operations.add(self._operation("operation-1", "key-1"))
            self.outbox.add(AggregateRef("Payment", payment.id, 1), [])
            self.inbox.register("message-1")
            raise RuntimeError("boom")

        with self.assertRaises(RuntimeError):
            self.uow.run(work)
        self.assertIsNone(self.payments.get(PaymentId("payment-1")))
        self.assertIsNone(self.operations.get(OperationId("operation-1")))
        self.assertTrue(self.inbox.register("message-1"))

    def test_mapped_violation_keeps_unit_of_work_usable(self) -> None:
        self._payment()
        self.operations.add(self._operation("operation-1", "key-1"))

        def work() -> str:
            with contextlib.suppress(DuplicateIdempotencyKeyError):
                self.operations.add(self._operation("operation-2", "key-1"))
            # Nested run joins the same transaction.
            return self.uow.run(lambda: str(self.operations.get(OperationId("operation-1"))))

        self.assertNotEqual(self.uow.run(work), "None")

    def test_outbox_writes_only_mapped_events_with_deterministic_id(self) -> None:
        payment = self._payment()

        def work() -> None:
            payment.authorize("auth-ref")
            payment.capture()
            events = payment.pull_events()
            version = self.payments.save(payment)
            self.outbox.add(AggregateRef("Payment", payment.id, version), events)

        self.uow.run(work)
        aggregate = AggregateRef("Payment", "payment-1", 2)
        self.assertEqual(
            self._outbox_rows(),
            [
                (
                    outbox_event_id(aggregate, AUTHORIZED_TYPE),
                    "Payment",
                    "payment-1",
                    2,
                    AUTHORIZED_TYPE,
                    PAYMENT_EVENTS_TOPIC,
                    b"authorized:payment-1",
                    0,
                    None,
                )
            ],
        )
        expected = uuid.uuid5(
            uuid.UUID("8f1d3c2a-5b7e-4a90-9c61-2e4f7b8d0a13"),
            f"Payment:payment-1:2:{AUTHORIZED_TYPE}",
        )
        self.assertEqual(outbox_event_id(aggregate, AUTHORIZED_TYPE), expected)

    def test_default_mapper_writes_nothing(self) -> None:
        payment = self._payment()
        payment.authorize("auth-ref")
        outbox = PostgresPaymentOutbox(self.database, NoPublishedContracts())
        self.uow.run(
            lambda: outbox.add(AggregateRef("Payment", payment.id, 2), payment.pull_events())
        )
        self.assertEqual(self._outbox_rows(), [])

    def test_duplicate_event_id_rolls_back(self) -> None:
        payment = self._payment()
        payment.authorize("auth-ref")
        events = payment.pull_events()
        ref = AggregateRef("Payment", payment.id, 2)
        self.uow.run(lambda: self.outbox.add(ref, events))
        with self.assertRaises(psycopg.errors.UniqueViolation):
            self.uow.run(lambda: self.outbox.add(ref, events))
        self.assertEqual(len(self._outbox_rows()), 1)

    def test_inbox_deduplicates_provider_messages(self) -> None:
        self.assertTrue(self.inbox.register("message-1"))
        self.assertFalse(self.inbox.register("message-1"))

    def test_authorize_use_case_over_postgres(self) -> None:
        payment = self._payment()
        provider = FakePaymentProvider()
        executor = PaymentOperationExecutor(
            uow=self.uow,
            payments=self.payments,
            operations=self.operations,
            outbox=self.outbox,
            provider=provider,
            permissions=FakePermissions(),
            clock=_Clock(),
            ids=_Ids(),
        )
        handler = AuthorizePaymentHandler(executor)
        command = AuthorizePaymentInput(
            subject=ORCHESTRATOR, payment_id=payment.id, idempotency_key="authorize-1"
        )
        first = handler.handle(command)
        replay = handler.handle(command)

        self.assertEqual(first.payment_state, PaymentState.AUTHORIZED)
        self.assertTrue(replay.replayed)
        self.assertEqual(len(provider.requests), 1)
        stored = self.payments.get(payment.id)
        assert stored is not None
        self.assertEqual((stored.state, stored.version), (PaymentState.AUTHORIZED, 2))
        rows = self._outbox_rows()
        self.assertEqual([row[4] for row in rows], [AUTHORIZED_TYPE])


if __name__ == "__main__":
    unittest.main()
