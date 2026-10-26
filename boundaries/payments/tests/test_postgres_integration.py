"""Integration with a real PostgreSQL: migrations, repositories, Unit of Work, Outbox, Inbox.

PostgreSQL runs in testcontainers (``tests/conftest.py``). Tables are truncated before
every test.
"""

import contextlib
import unittest
import uuid
from datetime import timedelta
from typing import Any, override

import psycopg
import pytest
from opentelemetry import trace
from payment_fakes import NOW, ORCHESTRATOR, FakePaymentProvider, FakePermissions

from infrastructure.repository import (
    PAYMENT_EVENTS_TOPIC,
    NoPublishedContracts,
    OutboxMessage,
    PostgresDatabase,
    PostgresPaymentOperationHistory,
    PostgresPaymentOperationRepository,
    PostgresPaymentOutbox,
    PostgresPaymentRepository,
    PostgresPendingOperationQuery,
    PostgresProviderMessageInbox,
    PostgresUnitOfWork,
    outbox_event_id,
)
from pkg.application import AggregateRef, ConcurrencyConflictError
from slices.payment.applications.authorize_payment import (
    AuthorizePaymentHandler,
    AuthorizePaymentInput,
)
from slices.payment.applications.operation_flow import (
    PaymentOperationExecutor,
    ProviderResultApplier,
)
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


@pytest.mark.usefixtures("postgres")
class PostgresIntegrationTest(unittest.TestCase):
    database: PostgresDatabase
    dsn: str

    @override
    def setUp(self) -> None:
        with self.database.connection() as conn:
            conn.execute("TRUNCATE payments, payment_operations, payments_outbox, inbox")
        self.uow = PostgresUnitOfWork(self.database)
        self.payments = PostgresPaymentRepository(self.database)
        self.operations = PostgresPaymentOperationRepository(self.database)
        self.pending = PostgresPendingOperationQuery(self.database)
        self.history = PostgresPaymentOperationHistory(self.database)
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
                "topic, payload, traceparent FROM payments_outbox ORDER BY created_at, event_id"
            ).fetchall()

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
        pending_id = self.pending.pending_operation_id(PaymentId("payment-1"))
        self.assertEqual(pending_id, OperationId("operation-1"))
        loaded = self.operations.get(OperationId("operation-1"))
        assert loaded is not None
        loaded.decline("insufficient funds", NOW + timedelta(minutes=2))
        self.assertEqual(self.operations.save(loaded), 2)
        with self.assertRaises(ConcurrencyConflictError):
            self.operations.save(loaded)
        self.assertIsNone(self.pending.pending_operation_id(PaymentId("payment-1")))
        self.operations.add(self._operation("operation-0", "key-0", minutes=0))

        history = self.history.list_for_payment(PaymentId("payment-1"))
        self.assertEqual([op.operation_id for op in history], ["operation-0", "operation-1"])
        self.assertEqual(history[1].decline_reason, "insufficient funds")
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
                    "",
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

    def test_outbox_table_matches_the_debezium_contract(self) -> None:
        with self.database.connection() as conn:
            columns = dict(
                conn.execute(
                    "SELECT column_name, column_default FROM information_schema.columns "
                    "WHERE table_name = 'payments_outbox'"
                ).fetchall()
            )
            published = conn.execute(
                "SELECT p.pubinsert, p.pubupdate, p.pubdelete, p.pubtruncate, t.tablename "
                "FROM pg_publication p JOIN pg_publication_tables t USING (pubname) "
                "WHERE pubname = 'payments_outbox'"
            ).fetchall()
        # ADR 37: только поля контракта и created_at для retention, без полей relay.
        self.assertEqual(
            set(columns),
            {
                "event_id",
                "event_type",
                "aggregate_type",
                "aggregate_id",
                "aggregate_version",
                "occurred_at",
                "topic",
                "payload",
                "traceparent",
                "created_at",
            },
        )
        self.assertEqual(columns["traceparent"], "''::text")
        self.assertEqual(published, [(True, False, False, False, "payments_outbox")])

    def test_outbox_row_fills_every_contract_column(self) -> None:
        payment = self._payment()
        payment.authorize("auth-ref")
        events = payment.pull_events()
        traceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
        span = trace.NonRecordingSpan(
            trace.SpanContext(
                int("0af7651916cd43dd8448eb211c80319c", 16),
                int("b7ad6b7169203331", 16),
                is_remote=False,
                trace_flags=trace.TraceFlags(trace.TraceFlags.SAMPLED),
            )
        )
        with trace.use_span(span):
            self.uow.run(lambda: self.outbox.add(AggregateRef("Payment", payment.id, 2), events))
        with self.database.connection() as conn:
            row = conn.execute(
                "SELECT occurred_at IS NOT NULL, occurred_at = created_at, traceparent "
                "FROM payments_outbox"
            ).fetchone()
        self.assertEqual(row, (True, True, traceparent))

    def test_outbox_write_deletes_rows_older_than_an_hour(self) -> None:
        payment = self._payment()
        with self.database.connection() as conn:
            conn.execute(
                "INSERT INTO payments_outbox (event_id, event_type, aggregate_type, "
                "aggregate_id, aggregate_version, occurred_at, topic, payload, created_at) "
                "VALUES (%s, 't', 'Payment', 'old', 1, now(), 'topic', '', "
                "now() - interval '61 minutes'), "
                "(%s, 't', 'Payment', 'recent', 1, now(), 'topic', '', "
                "now() - interval '59 minutes')",
                (uuid.uuid4(), uuid.uuid4()),
            )
        payment.authorize("auth-ref")
        events = payment.pull_events()
        self.uow.run(lambda: self.outbox.add(AggregateRef("Payment", payment.id, 2), events))
        self.assertEqual([row[2] for row in self._outbox_rows()], ["recent", "payment-1"])

    def test_retention_delete_rolls_back_with_the_write(self) -> None:
        payment = self._payment()
        with self.database.connection() as conn:
            conn.execute(
                "INSERT INTO payments_outbox (event_id, event_type, aggregate_type, "
                "aggregate_id, aggregate_version, occurred_at, topic, payload, created_at) "
                "VALUES (%s, 't', 'Payment', 'old', 1, now(), 'topic', '', "
                "now() - interval '2 hours')",
                (uuid.uuid4(),),
            )
        payment.authorize("auth-ref")
        events = payment.pull_events()

        def work() -> None:
            self.outbox.add(AggregateRef("Payment", payment.id, 2), events)
            raise RuntimeError("boom")

        with self.assertRaises(RuntimeError):
            self.uow.run(work)
        self.assertEqual([row[2] for row in self._outbox_rows()], ["old"])

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
            pending=self.pending,
            provider=provider,
            permissions=FakePermissions(),
            clock=_Clock(),
            operation_ids=_Ids(),
            applier=ProviderResultApplier(self.payments, self.operations, self.outbox, _Clock()),
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
