"""Rebuild and check of the data derived from the Payment streams over a real PostgreSQL."""

import io
import json
import os
import threading
import time
import unittest
from contextlib import redirect_stdout
from typing import Any, LiteralString, override
from unittest import mock

import psycopg
import pytest
from payment_fakes import NOW, ORCHESTRATOR, REQUESTER, PaymentsTestKit

from entrypoints.payments_admin import main as admin
from infrastructure.repository import Database
from slices.payment.applications.authorize_payment import (
    AuthorizePaymentHandler,
    AuthorizePaymentInput,
)
from slices.payment.applications.capture_payment import (
    CapturePaymentHandler,
    CapturePaymentInput,
)
from slices.payment.domains.payment import (
    OperationId,
    OperationKind,
    PaymentId,
)
from slices.payment.infrastructure.repository import (
    DatabasePaymentRepository,
    ProjectionMismatch,
    ProjectionRebuilder,
    UnreadableStoredDataError,
    projection_rebuild,
)
from slices.payment.infrastructure.repository.projection_rebuild import MismatchKind

SNAPSHOT_EVERY = 5
DERIVED = ("payments", "payment_operations", "payment_snapshots")


@pytest.mark.usefixtures("database")
class ProjectionRebuilderTest(unittest.TestCase):
    database: Database
    uri: str

    @override
    def setUp(self) -> None:
        self.kit = PaymentsTestKit(self.database)
        self.payments = DatabasePaymentRepository(self.database, SNAPSHOT_EVERY)
        self.rebuilder = ProjectionRebuilder(self.database, SNAPSHOT_EVERY)

    def captured_payment(self, name: str = "payment-1") -> PaymentId:
        """Payment with 7 events through the real use cases."""
        payment_id = self.kit.given_payment(payment_id=name, order_id=f"order-{name}")
        executor = self.kit.executor()
        AuthorizePaymentHandler(executor).handle(
            AuthorizePaymentInput(ORCHESTRATOR, payment_id, f"{name}:authorize")
        )
        CapturePaymentHandler(executor).handle(
            CapturePaymentInput(ORCHESTRATOR, payment_id, f"{name}:capture")
        )
        return payment_id

    def execute(self, query: LiteralString, *params: Any) -> None:
        with self.database.connection() as conn:
            conn.execute(query, params)

    def table(self, name: LiteralString) -> list[tuple[Any, ...]]:
        with self.database.connection() as conn:
            return conn.execute(f"SELECT * FROM {name} ORDER BY 1").fetchall()  # noqa: S608 - fixed table names

    def state(self) -> dict[str, list[tuple[Any, ...]]]:
        return {
            "payments": self.table("payments"),
            "payment_operations": self.table("payment_operations"),
        }

    def test_rebuild_restores_corrupted_projections(self) -> None:
        self.captured_payment("payment-1")
        self.captured_payment("payment-2")
        expected = self.state()
        self.execute("UPDATE payments SET state = 'failed', version = 99")
        self.execute("DELETE FROM payment_operations WHERE kind = 'capture'")
        self.execute(
            "INSERT INTO payments (id, order_id, market_id, amount, currency, state, version) "
            "VALUES ('ghost', 'order-ghost', 'berlin', 1, 'EUR', 'pending', 1)"
        )

        result = self.rebuilder.rebuild(snapshots=False)

        self.assertEqual((result.payments, result.operations, result.snapshots), (2, 4, 0))
        self.assertEqual(self.state(), expected)
        self.assertEqual(self.rebuilder.check(), [])

    def test_rebuild_ignores_snapshots_as_a_source(self) -> None:
        self.captured_payment()
        self.execute("UPDATE payment_snapshots SET state = '{\"broken\": true}'")

        self.rebuilder.rebuild(snapshots=False)

        with self.database.connection() as conn:
            row = conn.execute("SELECT state FROM payments").fetchone()
        self.assertEqual(row, ("captured",))

    def test_rebuild_with_snapshots_replaces_them_at_the_boundary(self) -> None:
        payment_id = self.captured_payment()
        self.execute("UPDATE payment_snapshots SET state = '{\"broken\": true}'")
        self.execute(
            "INSERT INTO payment_snapshots (payment_id, version, schema_version, state, "
            "recorded_at) VALUES ('ghost', 1, 1, '{}', now())"
        )

        result = self.rebuilder.rebuild(snapshots=True)

        self.assertEqual(result.snapshots, 1)
        rows = self.table("payment_snapshots")
        self.assertEqual([(row[0], row[1], row[2]) for row in rows], [(payment_id, 5, 4)])
        self.assertEqual(self.rebuilder.check(), [])
        stored = self.payments.get(payment_id)
        assert stored is not None
        self.assertEqual(stored.version, 7)

    def test_stream_below_the_boundary_gets_no_snapshot(self) -> None:
        self.kit.given_payment()
        self.assertEqual(self.rebuilder.rebuild(snapshots=True).snapshots, 0)
        self.assertEqual(self.table("payment_snapshots"), [])

    def test_unreadable_stream_rolls_everything_back(self) -> None:
        self.captured_payment()
        before = self.state()
        self.execute(
            "INSERT INTO payment_events (payment_id, version, event_type, schema_version, "
            "payload, traceparent, recorded_at) "
            "VALUES ('payment-1', 8, 'PaymentTeleported', 1, '{}', '', now())"
        )

        with self.assertRaises(UnreadableStoredDataError):
            self.rebuilder.rebuild(snapshots=True)

        self.assertEqual(self.state(), before)
        self.assertEqual(len(self.table("payment_snapshots")), 1)

    def test_rows_that_do_not_match_the_streams_roll_everything_back(self) -> None:
        self.captured_payment()
        before = self.state()

        with (
            mock.patch.object(projection_rebuild, "project_payment"),
            self.assertRaises(projection_rebuild.ProjectionRebuildError),
        ):
            self.rebuilder.rebuild(snapshots=False)

        self.assertEqual(self.state(), before)

    def test_locked_table_stops_the_rebuild_at_the_timeout(self) -> None:
        self.captured_payment()
        before = self.state()
        rebuilder = ProjectionRebuilder(self.database, SNAPSHOT_EVERY, lock_timeout="100ms")

        with psycopg.connect(self.uri) as holder:
            holder.execute("LOCK TABLE payments IN ROW EXCLUSIVE MODE")
            with self.assertRaises(psycopg.errors.LockNotAvailable):
                rebuilder.rebuild(snapshots=False)
            holder.rollback()

        self.assertEqual(self.state(), before)

    def test_outbox_and_streams_are_not_touched(self) -> None:
        self.captured_payment()
        events = self.table("payment_events")
        outbox = self.table("payments_outbox")

        self.rebuilder.rebuild(snapshots=True)

        self.assertEqual(self.table("payment_events"), events)
        self.assertEqual(self.table("payments_outbox"), outbox)

    def test_queries_read_old_rows_during_the_rebuild_and_writers_wait(self) -> None:
        """A writer that appended its events during the rebuild wins after its commit."""
        payment_id = self.kit.given_payment()
        operation = OperationId("op-1")
        writer_payment = self.payments.get(payment_id)
        assert writer_payment is not None
        writer_payment.request_operation(
            operation, OperationKind.AUTHORIZE, "key-1", NOW, REQUESTER
        )
        read_during_rebuild: list[Any] = []
        real_stream_ids = projection_rebuild.stream_ids

        def stream_ids_while_writer_waits(conn: psycopg.Connection) -> list[str]:
            writer.start()
            with psycopg.connect(self.uri, autocommit=True) as monitor:
                for _ in range(500):
                    waiting = monitor.execute(
                        "SELECT count(*) FROM pg_locks "
                        "WHERE NOT granted AND relation = 'payments'::regclass"
                    ).fetchone()
                    if waiting is not None and waiting[0] > 0:
                        break
                    time.sleep(0.01)
                else:
                    self.fail("the writer never waited for the lock")
                read_during_rebuild.extend(monitor.execute("SELECT id FROM payments").fetchall())
            return real_stream_ids(conn)

        writer = threading.Thread(target=self.payments.save, args=(writer_payment,))
        with mock.patch.object(projection_rebuild, "stream_ids", stream_ids_while_writer_waits):
            result = self.rebuilder.rebuild(snapshots=False)
        writer.join(10)

        self.assertEqual(read_during_rebuild, [(payment_id,)])
        self.assertEqual(result.operations, 0)
        self.assertEqual(self.table("payment_operations")[0][0], operation)
        self.assertEqual(self.rebuilder.check(), [])

    def test_check_reports_every_kind_of_difference(self) -> None:
        self.captured_payment("payment-1")
        self.captured_payment("payment-2")
        self.assertEqual(self.rebuilder.check(), [])
        before = self.state()

        self.execute("UPDATE payments SET state = 'failed' WHERE id = 'payment-1'")
        self.execute("DELETE FROM payment_operations WHERE idempotency_key = 'payment-2:capture'")
        self.execute(
            "INSERT INTO payments (id, order_id, market_id, amount, currency, state, version) "
            "VALUES ('ghost', 'order-ghost', 'berlin', 1, 'EUR', 'pending', 1)"
        )
        self.execute(
            "UPDATE payment_snapshots SET state = '{\"broken\": true}' WHERE payment_id = 'payment-2'"
        )

        mismatches = self.rebuilder.check()

        self.assertIn(ProjectionMismatch("payments", "payment-1", MismatchKind.DIFFERS), mismatches)
        self.assertIn(ProjectionMismatch("payments", "ghost", MismatchKind.EXTRA), mismatches)
        self.assertIn(
            ProjectionMismatch("payment_snapshots", "payment-2", MismatchKind.UNREADABLE),
            mismatches,
        )
        self.assertEqual(
            [m.kind for m in mismatches if m.table == "payment_operations"],
            [MismatchKind.MISSING],
        )
        self.assertNotEqual(self.state(), before)

    def test_check_finds_a_snapshot_with_a_wrong_state(self) -> None:
        self.captured_payment()
        self.execute(
            "UPDATE payment_snapshots SET state = jsonb_set(state, '{state}', '\"failed\"')"
        )
        self.assertEqual(
            self.rebuilder.check(),
            [ProjectionMismatch("payment_snapshots", "payment-1", MismatchKind.DIFFERS)],
        )

    def test_check_skips_a_snapshot_of_another_schema(self) -> None:
        self.captured_payment()
        self.execute("UPDATE payment_snapshots SET schema_version = 99, state = '{}'")
        self.assertEqual(self.rebuilder.check(), [])

    def test_check_changes_nothing(self) -> None:
        self.captured_payment()
        before = self.kit.snapshot()
        self.rebuilder.check()
        self.assertEqual(self.kit.snapshot(), before)

    def run_admin(self, *args: str) -> tuple[int, dict[str, Any]]:
        stdout = io.StringIO()
        env = {
            "DATABASE__URI": self.uri,
            "EVENT_STORE__SNAPSHOT_EVERY": str(SNAPSHOT_EVERY),
            "EVENT_STORE__SUBJECT_KEKS": '{"1": "kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk"}',
            "EVENT_STORE__SUBJECT_REF_KEY": "rrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrr",
        }
        with (
            mock.patch.dict(os.environ, env, clear=True),
            mock.patch.object(admin.telemetry, "configure_process_logging"),
            redirect_stdout(stdout),
        ):
            code = admin.main(list(args))
        return code, json.loads(stdout.getvalue())

    def test_admin_commands(self) -> None:
        self.captured_payment()
        self.assertEqual(self.run_admin("projections", "check"), (0, {"mismatches": []}))

        self.execute("UPDATE payments SET state = 'failed'")
        code, output = self.run_admin("projections", "check")
        self.assertEqual(code, 1)
        self.assertEqual(
            output["mismatches"],
            [{"table": "payments", "key": "payment-1", "kind": "differs"}],
        )

        code, output = self.run_admin("projections", "rebuild", "--snapshots")
        self.assertEqual(code, 0)
        self.assertEqual(output["rebuilt"], {"payments": 1, "operations": 2, "snapshots": 1})
        self.assertEqual(self.run_admin("projections", "check")[0], 0)

    def test_admin_requires_its_configuration(self) -> None:
        with (
            mock.patch.dict(os.environ, {}, clear=True),
            mock.patch.object(admin.telemetry, "configure_process_logging"),
            self.assertLogs(admin.logger, "ERROR"),
        ):
            self.assertEqual(admin.main(["projections", "check"]), 1)


class AdminArgumentsTest(unittest.TestCase):
    def test_unknown_command_is_rejected(self) -> None:
        with mock.patch("sys.stderr", io.StringIO()), self.assertRaises(SystemExit) as raised:
            admin.parse_args(["projections", "drop"])
        self.assertEqual(raised.exception.code, 2)

    def test_snapshots_are_opt_in(self) -> None:
        self.assertFalse(admin.parse_args(["projections", "rebuild"]).snapshots)
        self.assertTrue(admin.parse_args(["projections", "rebuild", "--snapshots"]).snapshots)
