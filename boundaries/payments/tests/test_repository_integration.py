"""Integration with a real PostgreSQL: migrations, Unit of Work, Outbox, Inbox, history.

The Payment streams are covered by ``test_payment_event_store_integration``.

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
from payment_fakes import (
    MARKET,
    NOW,
    ORCHESTRATOR,
    REQUESTER,
    SUBJECT_KEK,
    SUBJECT_REF_KEY,
    FakePaymentProvider,
    FakePermissions,
)

from infrastructure.repository import (
    PAYMENT_EVENTS_TOPIC,
    Database,
    DatabaseUnitOfWork,
    NoPublishedContracts,
    OutboxMessage,
    outbox_event_id,
)
from pkg.outbox import AggregateRef
from slices.payment.applications.authorize import (
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
    OperationId,
    OperationKind,
    OperationOutcome,
    OperationState,
    OrderId,
    Payment,
    PaymentAuthorized,
    PaymentDomainEvent,
    PaymentId,
    PaymentState,
)
from slices.payment.infrastructure.observability import FsmMetrics
from slices.payment.infrastructure.repository import (
    DatabasePaymentOperationHistory,
    DatabasePaymentOutbox,
    DatabasePaymentRepository,
    DatabaseProviderMessageInbox,
    DatabaseSubjectKeys,
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
SNAPSHOT_EVERY = 5


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


@pytest.mark.usefixtures("database")
class RepositoryIntegrationTest(unittest.TestCase):
    database: Database
    uri: str

    @override
    def setUp(self) -> None:
        with self.database.connection() as conn:
            conn.execute(
                "TRUNCATE payment_events, payment_snapshots, payments, payment_operations, "
                "payments_outbox, inbox"
            )
        self.uow = DatabaseUnitOfWork(self.database)
        self.payments = DatabasePaymentRepository(self.database, SNAPSHOT_EVERY)
        self.history = DatabasePaymentOperationHistory(self.database)
        self.outbox = DatabasePaymentOutbox(self.database, _AuthorizedOnly())
        self.inbox = DatabaseProviderMessageInbox(self.database)

    def _payment(self, payment_id: str = "payment-1", order_id: str = "order-1") -> Payment:
        payment = Payment(PaymentId(payment_id), OrderId(order_id), MARKET, AMOUNT)
        self.payments.add(payment)
        stored = self.payments.get(payment.id)
        assert stored is not None
        return stored

    def _authorized_events(self, payment: Payment) -> list[PaymentDomainEvent]:
        operation = OperationId(f"op-{payment.version}")
        payment.request_operation(
            operation, OperationKind.AUTHORIZE, f"key-{operation}", NOW, REQUESTER
        )
        return payment.complete_operation(operation, OperationOutcome.success("auth-ref"), NOW)

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
        self.assertEqual(payment.market_id, MARKET)
        self.assertEqual(str(payment.amount.amount), "25.00")
        self.assertEqual(payment.state, PaymentState.PENDING)
        by_order = self.payments.get_by_order(OrderId("order-1"))
        self.assertIsNotNone(by_order)
        self.assertIsNone(self.payments.get(PaymentId("missing")))

    def test_second_payment_for_order_is_duplicate(self) -> None:
        self._payment()
        with self.assertRaises(DuplicatePaymentError):
            self.payments.add(Payment(PaymentId("payment-2"), OrderId("order-1"), MARKET, AMOUNT))

    def test_completed_operation_frees_pending_slot_and_history_is_ordered(self) -> None:
        payment = self._payment()
        first = OperationId("operation-1")
        payment.request_operation(
            first, OperationKind.AUTHORIZE, "key-1", NOW + timedelta(minutes=1), REQUESTER
        )
        self.payments.save(payment)
        payment.complete_operation(
            first, OperationOutcome.declined("insufficient funds"), NOW + timedelta(minutes=2)
        )
        self.payments.save(payment)

        stored = self.payments.get(payment.id)
        assert stored is not None
        self.assertEqual(stored.state, PaymentState.FAILED)

        history = self.history.list_for_payment(PaymentId("payment-1"))
        assert history is not None
        self.assertIsNone(self.history.list_for_payment(PaymentId("missing")))
        self.assertEqual([op.operation_id for op in history], ["operation-1"])
        self.assertEqual(history[0].decline_reason, "insufficient funds")
        declined = stored.model.operation_by_key("key-1")
        assert declined is not None
        self.assertEqual(declined.state, OperationState.DECLINED)
        self.assertEqual(declined.decline_reason, "insufficient funds")
        self.assertEqual(declined.completed_at, NOW + timedelta(minutes=2))

    def test_unit_of_work_rolls_back_every_repository(self) -> None:
        def work() -> None:
            payment = Payment(PaymentId("payment-1"), OrderId("order-1"), MARKET, AMOUNT)
            self.payments.add(payment)
            self.outbox.add(AggregateRef("Payment", payment.id, 1), [])
            self.inbox.register("message-1")
            raise RuntimeError("boom")

        with self.assertRaises(RuntimeError):
            self.uow.run(work)
        self.assertIsNone(self.payments.get(PaymentId("payment-1")))
        self.assertTrue(self.inbox.register("message-1"))

    def test_mapped_violation_keeps_unit_of_work_usable(self) -> None:
        self._payment()
        other = self._payment("payment-2", "order-2")
        other.request_operation(
            OperationId("op-1"), OperationKind.AUTHORIZE, "key-1", NOW, REQUESTER
        )
        self.payments.save(other)

        def work() -> str:
            payment = self.payments.get(PaymentId("payment-1"))
            assert payment is not None
            payment.request_operation(
                OperationId("op-2"), OperationKind.AUTHORIZE, "key-1", NOW, REQUESTER
            )
            with contextlib.suppress(DuplicateIdempotencyKeyError):
                self.payments.save(payment)
            # Nested run joins the same transaction.
            return self.uow.run(lambda: str(self.payments.get_by_idempotency_key("key-1")))

        self.assertNotEqual(self.uow.run(work), "None")

    def test_outbox_writes_only_mapped_events_with_deterministic_id(self) -> None:
        payment = self._payment()

        def work() -> None:
            events = self._authorized_events(payment)
            version = self.payments.save(payment)
            self.outbox.add(AggregateRef("Payment", payment.id, version), events)

        self.uow.run(work)
        aggregate = AggregateRef("Payment", "payment-1", 4)
        self.assertEqual(
            self._outbox_rows(),
            [
                (
                    outbox_event_id(aggregate, AUTHORIZED_TYPE),
                    "Payment",
                    "payment-1",
                    4,
                    AUTHORIZED_TYPE,
                    PAYMENT_EVENTS_TOPIC,
                    b"authorized:payment-1",
                    "",
                )
            ],
        )
        expected = uuid.uuid5(
            uuid.UUID("8f1d3c2a-5b7e-4a90-9c61-2e4f7b8d0a13"),
            f"Payment:payment-1:4:{AUTHORIZED_TYPE}",
        )
        self.assertEqual(outbox_event_id(aggregate, AUTHORIZED_TYPE), expected)

    def test_default_mapper_writes_nothing(self) -> None:
        payment = self._payment()
        events = self._authorized_events(payment)
        outbox = DatabasePaymentOutbox(self.database, NoPublishedContracts())
        self.uow.run(lambda: outbox.add(AggregateRef("Payment", payment.id, 2), events))
        self.assertEqual(self._outbox_rows(), [])

    def test_duplicate_event_id_rolls_back(self) -> None:
        payment = self._payment()
        events = self._authorized_events(payment)
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
        events = self._authorized_events(payment)
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
        events = self._authorized_events(payment)
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
        events = self._authorized_events(payment)

        def work() -> None:
            self.outbox.add(AggregateRef("Payment", payment.id, 2), events)
            raise RuntimeError("boom")

        with self.assertRaises(RuntimeError):
            self.uow.run(work)
        self.assertEqual([row[2] for row in self._outbox_rows()], ["old"])

    def test_inbox_deduplicates_provider_messages(self) -> None:
        self.assertTrue(self.inbox.register("message-1"))
        self.assertFalse(self.inbox.register("message-1"))

    def test_authorize_use_case_over_the_database(self) -> None:
        payment = self._payment()
        provider = FakePaymentProvider()
        executor = PaymentOperationExecutor(
            uow=self.uow,
            payments=self.payments,
            provider=provider,
            permissions=FakePermissions(),
            clock=_Clock(),
            operation_ids=_Ids(),
            applier=ProviderResultApplier(self.payments, self.outbox, _Clock(), FsmMetrics()),
            transitions=FsmMetrics(),
            subjects=DatabaseSubjectKeys(self.database, {1: SUBJECT_KEK}, SUBJECT_REF_KEY),
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
        self.assertEqual((stored.state, stored.version), (PaymentState.AUTHORIZED, 4))
        rows = self._outbox_rows()
        self.assertEqual([row[4] for row in rows], [AUTHORIZED_TYPE])


if __name__ == "__main__":
    unittest.main()
