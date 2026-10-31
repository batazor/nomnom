"""The Payment streams over a real PostgreSQL: FR-742–FR-744, FR-746, FR-747, FR-749."""

import contextlib
import json
import threading
import unittest
from pathlib import Path
from typing import Any, LiteralString, override
from unittest import mock

import psycopg
import pytest
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import HistogramDataPoint, InMemoryMetricReader
from payment_fakes import MARKET, NOW, ORCHESTRATOR, REQUESTER, PaymentsTestKit
from psycopg.types.json import Jsonb

from infrastructure.repository import Database
from pkg.usecase import ConcurrencyConflictError
from slices.payment.applications.authorize import (
    AuthorizePaymentHandler,
    AuthorizePaymentInput,
)
from slices.payment.applications.errors import (
    IdempotencyKeyConflictError,
    OperationInProgressError,
    PaymentStateConflictError,
)
from slices.payment.domains.payment import (
    DuplicatePaymentError,
    Money,
    OperationId,
    OperationKind,
    OperationOutcome,
    OrderId,
    Payment,
    PaymentId,
    PaymentState,
)
from slices.payment.infrastructure.repository import (
    DatabasePaymentRepository,
    ReplayMetrics,
    UnreadableStoredDataError,
    repositories,
)
from slices.payment.infrastructure.repository.event_codec import encode_snapshot
from slices.payment.infrastructure.repository.repositories import project_payment

STREAMS = Path(__file__).parent / "fixtures" / "payment_streams"
AMOUNT = Money.parse("25.00", "EUR")
PAYMENT = PaymentId("payment-1")
ORDER = OrderId("order-1")
EVERY = 5


def new_payment(payment_id: str = PAYMENT, order_id: str = ORDER) -> Payment:
    return Payment(PaymentId(payment_id), OrderId(order_id), MARKET, AMOUNT)


@pytest.mark.usefixtures("database")
class PaymentEventStoreTest(unittest.TestCase):
    database: Database
    uri: str

    @override
    def setUp(self) -> None:
        self.kit = PaymentsTestKit(self.database)
        self.reader = InMemoryMetricReader()
        meter = MeterProvider(metric_readers=[self.reader]).get_meter("test")
        self.payments = DatabasePaymentRepository(self.database, EVERY, ReplayMetrics(meter))

    def rows(self, query: LiteralString, *params: Any) -> list[tuple[Any, ...]]:
        with self.database.connection() as conn:
            return conn.execute(query, params).fetchall()

    def stream(self, payment_id: str = PAYMENT) -> list[str]:
        rows = self.rows(
            "SELECT event_type FROM payment_events WHERE payment_id = %s ORDER BY version",
            payment_id,
        )
        return [row[0] for row in rows]

    def stored(self, payment_id: str = PAYMENT) -> Payment:
        payment = self.payments.get(PaymentId(payment_id))
        assert payment is not None
        return payment

    def authorized(self) -> Payment:
        """Payment with 4 events: Created, Requested, Succeeded, Authorized."""
        self.payments.add(new_payment())
        payment = self.stored()
        payment.request_operation(
            OperationId("op-a"), OperationKind.AUTHORIZE, "key-a", NOW, REQUESTER
        )
        self.payments.save(payment)
        payment = self.stored()
        payment.complete_operation(OperationId("op-a"), OperationOutcome.success("auth-1"), NOW)
        self.payments.save(payment)
        return self.stored()

    def snapshot_row(self, payment_id: str = PAYMENT) -> tuple[Any, ...] | None:
        rows = self.rows(
            "SELECT version, schema_version FROM payment_snapshots WHERE payment_id = %s",
            payment_id,
        )
        return rows[0] if rows else None

    def test_ac_1017_ac_1020_stream_is_the_source_of_truth(self) -> None:
        self.assertEqual(self.payments.add(new_payment()), 1)
        self.assertEqual(self.stream(), ["PaymentCreated"])

        # Проекцию можно подделать: состояние читается из потока.
        with self.database.connection() as conn:
            conn.execute("UPDATE payments SET state = 'captured' WHERE id = %s", (PAYMENT,))
        self.assertEqual(self.stored().state, PaymentState.PENDING)

    def test_ac_1018_unknown_payment_is_none(self) -> None:
        self.assertIsNone(self.payments.get(PaymentId("missing")))
        self.assertIsNone(self.payments.get_by_order(OrderId("missing")))
        self.assertIsNone(self.payments.get_by_operation(OperationId("missing")))
        self.assertIsNone(self.payments.get_by_idempotency_key("missing"))

    def test_lookups_find_the_payment_through_the_projections(self) -> None:
        self.authorized()
        for found in (
            self.payments.get_by_order(ORDER),
            self.payments.get_by_operation(OperationId("op-a")),
            self.payments.get_by_idempotency_key("key-a"),
        ):
            assert found is not None
            self.assertEqual((found.id, found.state), (PAYMENT, PaymentState.AUTHORIZED))

    def test_ac_1021_stream_of_a_successful_authorization(self) -> None:
        payment = self.authorized()
        self.assertEqual(payment.version, 4)
        self.assertEqual(payment.provider_reference, "auth-1")
        self.assertEqual(
            self.stream(),
            [
                "PaymentCreated",
                "PaymentOperationRequested",
                "PaymentOperationSucceeded",
                "PaymentAuthorized",
            ],
        )

    def test_ac_1025_stream_is_append_only(self) -> None:
        self.payments.add(new_payment())
        for statement in (
            "UPDATE payment_events SET event_type = 'x'",
            "DELETE FROM payment_events",
        ):
            with (
                self.subTest(statement),
                self.assertRaisesRegex(psycopg.errors.RaiseException, "append-only"),
                self.database.connection() as conn,
            ):
                conn.execute(statement)
        self.assertEqual(self.stream(), ["PaymentCreated"])

    def test_ac_1026_stale_write_conflicts_and_leaves_no_events(self) -> None:
        self.payments.add(new_payment())
        first, second = self.stored(), self.stored()
        first.request_operation(
            OperationId("op-1"), OperationKind.AUTHORIZE, "key-1", NOW, REQUESTER
        )
        second.request_operation(
            OperationId("op-2"), OperationKind.AUTHORIZE, "key-2", NOW, REQUESTER
        )

        self.assertEqual(self.payments.save(first), 2)
        with self.assertRaises(ConcurrencyConflictError):
            self.payments.save(second)

        self.assertEqual(self.stream(), ["PaymentCreated", "PaymentOperationRequested"])
        self.assertEqual(self.rows("SELECT id FROM payment_operations"), [("op-1",)])

    def test_save_without_new_events_writes_nothing(self) -> None:
        self.payments.add(new_payment())
        payment = self.stored()
        self.assertEqual(self.payments.save(payment), 1)
        self.assertEqual(self.stream(), ["PaymentCreated"])

    def concurrently(self, keys: tuple[str, str]) -> None:
        """Authorize one Payment from two threads started together."""
        payment_id = self.kit.given_payment(payment_id="payment-c", order_id="order-c")
        handler = AuthorizePaymentHandler(self.kit.executor())
        barrier = threading.Barrier(len(keys))

        def run(key: str) -> None:
            barrier.wait()
            # Проигравший видит незавершённую операцию или уже авторизованный Payment:
            # любой из отказов допустим, второго финансового эффекта быть не должно.
            with contextlib.suppress(
                OperationInProgressError, PaymentStateConflictError, ConcurrencyConflictError
            ):
                handler.handle(AuthorizePaymentInput(ORCHESTRATOR, payment_id, key))

        threads = [threading.Thread(target=run, args=(key,)) for key in keys]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join(10)

    def test_ac_1027_concurrent_requests_with_different_keys(self) -> None:
        self.concurrently(("left", "right"))

        self.assertEqual(len(self.kit.stored_operations()), 1)
        self.assertEqual(self.kit.provider.effect_count(OperationKind.AUTHORIZE), 1)
        self.assertEqual(self.stream("payment-c").count("PaymentOperationRequested"), 1)
        self.assertEqual(self.stream("payment-c").count("PaymentAuthorized"), 1)

    def test_ac_1028_concurrent_requests_with_one_key(self) -> None:
        self.concurrently(("same", "same"))

        self.assertEqual(len(self.kit.stored_operations()), 1)
        self.assertEqual(self.kit.provider.effect_count(OperationKind.AUTHORIZE), 1)
        self.assertEqual(self.stream("payment-c").count("PaymentOperationRequested"), 1)
        self.assertEqual(self.stream("payment-c").count("PaymentAuthorized"), 1)

    def test_ac_1029_projection_equals_the_stream(self) -> None:
        payment = self.authorized()
        row = self.rows(
            "SELECT state, provider_reference, version FROM payments WHERE id = %s", PAYMENT
        )
        self.assertEqual(row, [("authorized", "auth-1", payment.version)])
        (operation,) = payment.operations
        self.assertEqual(
            self.rows("SELECT id, state, provider_reference, completed_at FROM payment_operations"),
            [(operation.id, operation.state.value, "auth-1", operation.completed_at)],
        )

    def test_ac_1030_second_payment_of_the_order_rolls_its_stream_back(self) -> None:
        self.payments.add(new_payment())

        with self.assertRaises(DuplicatePaymentError):
            self.payments.add(new_payment("payment-2"))

        self.assertEqual(self.stream("payment-2"), [])
        self.assertEqual(self.rows("SELECT id FROM payments"), [(PAYMENT,)])

    def test_ac_1031_key_of_another_payment_rolls_events_back(self) -> None:
        first = self.kit.given_payment(payment_id="payment-a", order_id="order-a")
        second = self.kit.given_payment(payment_id="payment-b", order_id="order-b")
        self.kit.given_operation(first, OperationKind.AUTHORIZE, "shared", operation_id="op-a")
        handler = AuthorizePaymentHandler(self.kit.executor())

        with self.assertRaises(IdempotencyKeyConflictError):
            handler.handle(AuthorizePaymentInput(ORCHESTRATOR, second, "shared"))

        self.assertEqual(self.stream("payment-b"), ["PaymentCreated"])
        self.assertEqual(self.kit.provider.requests, [])

    def test_ac_1034_no_snapshot_below_the_boundary(self) -> None:
        self.authorized()
        self.assertIsNone(self.snapshot_row())

    def test_ac_1035_commit_jumping_over_the_boundary_writes_a_snapshot(self) -> None:
        self.authorized()
        payment = self.stored()
        payment.request_operation(
            OperationId("op-c"), OperationKind.CAPTURE, "key-c", NOW, REQUESTER
        )
        payment.complete_operation(OperationId("op-c"), OperationOutcome.success(), NOW)
        # Одним commit: Requested, Succeeded, Captured — версии 5, 6, 7.
        self.assertEqual(self.payments.save(payment), 7)
        self.assertEqual(self.snapshot_row(), (7, 4))
        self.assertEqual(self.rows("SELECT count(*) FROM payment_snapshots"), [(1,)])

    def test_ac_1036_rollback_keeps_neither_events_nor_snapshot(self) -> None:
        self.authorized()
        payment = self.stored()
        payment.request_operation(
            OperationId("op-c"), OperationKind.CAPTURE, "key-c", NOW, REQUESTER
        )

        def work() -> None:
            self.payments.save(payment)
            raise RuntimeError("boom")

        with self.assertRaises(RuntimeError):
            self.kit.uow.run(work)

        self.assertEqual(len(self.stream()), 4)
        self.assertIsNone(self.snapshot_row())

    def test_failed_snapshot_write_rolls_the_events_back(self) -> None:
        self.authorized()
        payment = self.stored()
        payment.request_operation(
            OperationId("op-c"), OperationKind.CAPTURE, "key-c", NOW, REQUESTER
        )

        with (
            mock.patch.object(repositories, "write_snapshot", side_effect=RuntimeError("boom")),
            self.assertRaises(RuntimeError),
        ):
            self.payments.save(payment)

        self.assertEqual(len(self.stream()), 4)
        self.assertEqual(
            self.rows("SELECT count(*) FROM payment_operations WHERE id = 'op-c'"), [(0,)]
        )

    def snapshotted(self) -> Payment:
        """Payment with a snapshot of version 5 and a stream of 6 events."""
        self.authorized()
        payment = self.stored()
        payment.request_operation(
            OperationId("op-c"), OperationKind.CAPTURE, "key-c", NOW, REQUESTER
        )
        self.payments.save(payment)
        payment = self.stored()
        payment.complete_operation(OperationId("op-c"), OperationOutcome.declined("no"), NOW)
        self.payments.save(payment)
        return self.stored()

    def replayed(self) -> list[tuple[float, bool]]:
        data = self.reader.get_metrics_data()
        assert data is not None
        loads: list[tuple[float, bool]] = []
        for resource in data.resource_metrics:
            for scope in resource.scope_metrics:
                for metric in scope.metrics:
                    self.assertEqual(metric.name, "nomnom.payments.event_store.replayed_events")
                    for point in metric.data.data_points:
                        assert isinstance(point, HistogramDataPoint)
                        attributes = dict(point.attributes or {})
                        self.assertEqual(set(attributes), {"nomnom.event_store.from_snapshot"})
                        loads.append(
                            (point.sum, bool(attributes["nomnom.event_store.from_snapshot"]))
                        )
        return loads

    def test_ac_1038_load_applies_only_the_tail(self) -> None:
        full = self.snapshotted()
        self.assertEqual(self.snapshot_row(), (5, 4))
        self.reader.get_metrics_data()

        with mock.patch.object(repositories, "read_events", wraps=repositories.read_events) as read:
            loaded = self.stored()

        self.assertEqual(read.call_args.args[2], 5)
        self.assertEqual(loaded.model, full.model)
        self.assertEqual(loaded.version, 6)
        self.assertIn(True, [from_snapshot for _, from_snapshot in self.replayed()])

    def test_ac_1039_deleted_snapshot_loses_nothing(self) -> None:
        full = self.snapshotted()
        with self.database.connection() as conn:
            conn.execute("TRUNCATE payment_snapshots")

        with mock.patch.object(repositories, "read_events", wraps=repositories.read_events) as read:
            loaded = self.stored()

        self.assertEqual(read.call_args.args[2], 0)
        self.assertEqual(loaded.model, full.model)

    def test_ac_1040_snapshot_of_another_schema_is_replaced(self) -> None:
        full = self.snapshotted()
        with self.database.connection() as conn:
            conn.execute(
                "UPDATE payment_snapshots SET schema_version = 99, state = '{\"old\": true}'"
            )

        loaded = self.stored()
        self.assertEqual(loaded.model, full.model)

        # Следующая граница (версия 10) перезаписывает снапшот текущей версией схемы.
        for key in ("x-1", "x-2"):
            payment = self.stored()
            operation = OperationId(f"op-{key}")
            payment.request_operation(operation, OperationKind.CAPTURE, key, NOW, REQUESTER)
            payment.complete_operation(operation, OperationOutcome.declined("no"), NOW)
            self.payments.save(payment)
        self.assertEqual(self.snapshot_row(), (10, 4))
        self.assertEqual(self.stored().version, 10)

    def test_ac_1041_unreadable_snapshot_of_the_current_schema_is_an_error(self) -> None:
        self.snapshotted()
        with self.database.connection() as conn:
            conn.execute("UPDATE payment_snapshots SET state = '{\"old\": true}'")

        with self.assertRaises(UnreadableStoredDataError):
            self.stored()

    def test_data_recorded_before_requested_by_keeps_working(self) -> None:
        """Events and a snapshot of the old schemas, then a new operation on top of them."""
        fixture = STREAMS / "captured_and_refunded.json"
        recorded = json.loads(fixture.read_text())["events"][:4]
        with self.database.connection() as conn:
            for version, event in enumerate(recorded, start=1):
                conn.execute(
                    "INSERT INTO payment_events (payment_id, version, event_type, schema_version, "
                    "payload, traceparent, recorded_at) VALUES ('payment-g', %s, %s, %s, %s, '', now())",
                    (
                        version,
                        event["event_type"],
                        event["schema_version"],
                        Jsonb(event["payload"]),
                    ),
                )
        loaded = self.stored("payment-g")
        old_state = encode_snapshot(loaded.model)
        old_state["operations"][0]["requested_by"] = None
        with self.database.connection() as conn:
            project_payment(conn, loaded.model, loaded.version)
            conn.execute(
                "INSERT INTO payment_snapshots (payment_id, version, schema_version, state, "
                "recorded_at) VALUES ('payment-g', 4, 1, %s, now())",
                (Jsonb(old_state),),
            )

        # Снапшот старой схемы не используется, и его отсутствие в разборе — не ошибка.
        payment = self.stored("payment-g")
        self.assertEqual(payment.version, 4)
        payment.request_operation(
            OperationId("op-c"), OperationKind.CAPTURE, "key-c", NOW, REQUESTER
        )
        self.assertEqual(self.payments.save(payment), 5)

        self.assertEqual(self.snapshot_row("payment-g"), (5, 4))
        view = self.kit.audit.history(PaymentId("payment-g"))
        assert view is not None
        self.assertEqual([op.requested_by for op in view.operations], [None, REQUESTER])
        self.assertEqual(
            [
                row[0]
                for row in self.rows(
                    "SELECT schema_version FROM payment_events WHERE payment_id = %s "
                    "AND event_type = 'PaymentOperationRequested' ORDER BY version",
                    "payment-g",
                )
            ],
            [1, 3],
        )

    def test_gap_in_the_stream_is_an_error(self) -> None:
        self.authorized()
        with self.database.connection() as conn:
            conn.execute("ALTER TABLE payment_events DISABLE TRIGGER payment_events_append_only")
            try:
                conn.execute("DELETE FROM payment_events WHERE version = 2")
            finally:
                conn.execute("ALTER TABLE payment_events ENABLE TRIGGER payment_events_append_only")

        with self.assertRaisesRegex(UnreadableStoredDataError, "gap"):
            self.stored()

    def test_ac_1042_ac_1043_outbox_receives_only_published_facts(self) -> None:
        payment_id = self.kit.given_payment(payment_id="payment-o", order_id="order-o")
        handler = AuthorizePaymentHandler(self.kit.executor())

        handler.handle(AuthorizePaymentInput(ORCHESTRATOR, payment_id, "key-o"))

        self.assertEqual(self.stream("payment-o")[-1], "PaymentAuthorized")
        (aggregate,) = self.kit.outbox_aggregates()
        self.assertEqual(aggregate.version, 4)
        self.assertEqual(
            [type(event).__name__ for event in self.kit.published()], ["PaymentAuthorized"]
        )


if __name__ == "__main__":
    unittest.main()
