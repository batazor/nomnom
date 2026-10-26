import http.client
import io
import json
import logging
import os
import threading
import time
import unittest
import uuid
from collections.abc import Iterator
from contextlib import redirect_stdout
from pathlib import Path
from typing import override
from unittest.mock import patch

import psycopg
import pytest
from dependency_injector import providers
from payment_fakes import (
    NOW,
    PaymentsTestKit,
    close_container,
    payments_container,
)
from psycopg import sql

from di.payments import PaymentsContainer, SpiceDBConfigurationError
from di.resources import MIGRATIONS, ProcessResource, TelemetryResource, postgres_database
from entrypoints.payments import main as payments_main
from infrastructure.authorization import SpiceDBPermissionChecker
from infrastructure.grpc.service import GrpcWorker, PaymentsHandlers
from infrastructure.messaging import PaymentEventsMapper
from infrastructure.observability import ObservedOutbox, ObservedUnitOfWork
from infrastructure.provider import (
    SIGNATURE_HEADER,
    WEBHOOK_PATH,
    FakePaymentProvider,
    sign_payload,
)
from infrastructure.repository import (
    MigrationSet,
    MissingMigrationsError,
    PostgresDatabase,
    PostgresPaymentOutbox,
    migrate,
)
from infrastructure.repository.migrate import _LOCK_KEY
from pkg.application import Subject, SubjectKind
from pkg.config import DatabaseSettings
from pkg.lifecycle import HttpWorker, WorkerGroup
from slices.payment.applications.create_payment import CreatePaymentInput
from slices.payment.domains.payment import OperationKind, OperationState, PaymentState

SPICEDB_ENV = {
    "SPICEDB__ENDPOINT": "127.0.0.1:1",
    "SPICEDB__PRESHARED_KEY": "devkey",
    "SPICEDB__INSECURE": "true",
}
SECRET = "whsec_test"
_FIXTURES = Path(__file__).resolve().parents[1].parent.parent / "tests" / "identity" / "fixtures"
_VECTORS = json.loads((_FIXTURES / "vectors.json").read_text(encoding="utf-8"))
# Process settings the payments container requires besides SpiceDB.
PROCESS_ENV = {
    "IDENTITY__ISSUER": _VECTORS["issuer"],
    "IDENTITY__AUDIENCE": _VECTORS["audience"],
    "IDENTITY__JWKS_FILE": str(_FIXTURES / "jwks.json"),
    "IDENTITY__SERVICE_ACCOUNTS": _VECTORS["service_accounts"],
    "PROVIDER__KIND": "fake",
    "PROVIDER__WEBHOOK_SECRET": SECRET,
}
WORKER = Subject(SubjectKind.SERVICE, "orders-worker")


def _shutdown_workers(container: PaymentsContainer) -> None:
    container.grpc_worker().shutdown(time.monotonic() + 1)


@pytest.mark.usefixtures("postgres")
class PaymentsContainerTest(unittest.TestCase):
    database: PostgresDatabase

    def container(self, **environment: str) -> PaymentsContainer:
        container = payments_container({**SPICEDB_ENV, **PROCESS_ENV, **environment})
        self.addCleanup(close_container, container)
        return container

    def test_resolves_the_process_graph(self) -> None:
        container = self.container(PROVIDER__KIND="fake")
        workers = container.workers()
        self.addCleanup(_shutdown_workers, container)

        self.assertIsInstance(workers, WorkerGroup)
        self.assertIsInstance(container.grpc_worker(), GrpcWorker)
        self.assertIsInstance(container.webhook_worker(), HttpWorker)
        self.assertIsInstance(container.handlers(), PaymentsHandlers)
        self.assertIsInstance(container.permissions(), SpiceDBPermissionChecker)
        self.assertIsInstance(container.provider().inner, FakePaymentProvider)
        # Business counters wrap the Outbox and the Unit of Work (ops/semconv domain.yaml).
        self.assertIsInstance(container.outbox(), ObservedOutbox)
        self.assertIsInstance(container.outbox()._inner, PostgresPaymentOutbox)
        self.assertIsInstance(container.uow(), ObservedUnitOfWork)
        self.assertIsInstance(container.outbox_mapper(), PaymentEventsMapper)
        # Process-wide singletons: one sandbox provider dedupes by idempotency key.
        self.assertIs(container.handlers(), container.handlers())
        self.assertIs(container.provider(), container.provider())
        self.assertIs(container.workers(), workers)

    def test_empty_spicedb_endpoint_fails_startup(self) -> None:
        container = payments_container(PROCESS_ENV)
        self.addCleanup(close_container, container)
        with self.assertRaisesRegex(SpiceDBConfigurationError, "SPICEDB__ENDPOINT is required"):
            container.workers()

    def test_override_substitutes_fakes_in_use_cases(self) -> None:
        kit = PaymentsTestKit(self.database)
        container = self.container()
        for name, fake in {
            "uow": kit.uow,
            "payments": kit.payments,
            "operations": kit.operations,
            "outbox": kit.outbox,
            "provider": kit.provider,
            "permissions": kit.permissions,
            "clock": kit.clock,
            "ids": kit.ids,
        }.items():
            getattr(container, name).override(providers.Object(fake))

        result = container.handlers().create_payment.handle(
            CreatePaymentInput(subject=WORKER, order_id="order-1", amount="25.00", currency="EUR")
        )

        self.assertTrue(result.created)
        self.assertEqual(kit.payment(result.payment_id).state, PaymentState.PENDING)
        self.assertEqual(len(kit.permissions.checks), 1)

    def test_webhook_worker_applies_callbacks_through_overridden_storage(self) -> None:
        kit = PaymentsTestKit(self.database)
        payment_id = kit.given_payment(PaymentState.AUTHORIZED)
        operation_id = kit.given_operation(payment_id, OperationKind.CAPTURE, "order-1:capture")
        container = self.container(PROVIDER__WEBHOOK_SECRET=SECRET)
        for name, fake in {
            "uow": kit.uow,
            "payments": kit.payments,
            "operations": kit.operations,
            "outbox": kit.outbox,
            "inbox": kit.inbox,
            "clock": kit.clock,
        }.items():
            getattr(container, name).override(providers.Object(fake))
        worker = container.webhook_worker()
        worker.start()
        thread = threading.Thread(target=worker.run, daemon=True)
        thread.start()
        self.addCleanup(thread.join, 2)
        self.addCleanup(worker.shutdown, time.monotonic() + 2)

        body = json.dumps(
            {
                "id": "msg-1",
                "operation_id": str(operation_id),
                "status": "succeeded",
                "provider_reference": "capture-ref",
            }
        ).encode()
        connection = http.client.HTTPConnection("127.0.0.1", worker.port, timeout=5)
        try:
            signature = sign_payload(SECRET, int(NOW.timestamp()), body)
            connection.request("POST", WEBHOOK_PATH, body, {SIGNATURE_HEADER: signature})
            response = connection.getresponse()
            self.assertEqual(
                (response.status, json.loads(response.read())["status"]), (200, "applied")
            )
        finally:
            connection.close()
        self.assertEqual(kit.operation(operation_id).state, OperationState.SUCCEEDED)


@pytest.mark.usefixtures("postgres")
class StartupMigrationTest(unittest.TestCase):
    """The database resource applies pending migrations before the process serves."""

    dsn: str

    def start(self, dsn: str) -> list[str]:
        resource = ProcessResource(postgres_database, DatabaseSettings(DATABASE__URL=dsn))
        with self.assertLogs("di.resources", logging.INFO) as logs:
            resource.init()
        resource.shutdown()
        return logs.output

    def test_migrated_database_is_left_unchanged(self) -> None:
        with psycopg.connect(self.dsn) as conn:
            before = _applied(conn)
        logs = self.start(self.dsn)
        self.assertIn("payments migrations applied: none pending", logs[-1])
        with psycopg.connect(self.dsn) as conn:
            self.assertEqual(_applied(conn), before)

    def test_empty_database_gets_every_migration_shared_tables_first(self) -> None:
        dsn = self._empty_database()
        logs = self.start(dsn)
        self.assertEqual(
            [migration_set.owner for migration_set in MIGRATIONS], ["infrastructure", "payment"]
        )
        self.assertIn(f"payments migrations applied: {', '.join(_ids())}", logs[-1])
        with psycopg.connect(dsn) as conn:
            self.assertEqual(
                {owner: [row[0] for row in rows] for owner, rows in _applied(conn).items()},
                {
                    migration_set.owner: [m.id for m in migration_set.migrations()]
                    for migration_set in MIGRATIONS
                },
            )

    def test_every_set_numbers_its_migrations_from_one(self) -> None:
        for migration_set in MIGRATIONS:
            with self.subTest(owner=migration_set.owner):
                self.assertTrue(migration_set.migrations()[0].id.startswith("00001_"))

    def test_set_without_migrations_fails(self) -> None:
        with self.assertRaises(MissingMigrationsError):
            MigrationSet("empty", "pkg").migrations()

    def test_replicas_starting_together_apply_migrations_once(self) -> None:
        dsn = self._empty_database()
        settings = DatabaseSettings(DATABASE__URL=dsn)
        results: list[list[str]] = []
        threads = [
            threading.Thread(target=lambda: results.append(migrate(settings, MIGRATIONS)))
            for _ in range(4)
        ]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        self.assertEqual(sorted(results, key=len), [[], [], [], _ids()])
        with psycopg.connect(dsn) as conn:
            count = sum(len(rows) for rows in _applied(conn).values())
        self.assertEqual(count, len(_ids()))

    def test_lock_of_a_killed_process_is_released_with_its_connection(self) -> None:
        dsn = self._empty_database()
        holder = psycopg.connect(dsn, autocommit=True)
        holder.execute("SELECT pg_advisory_lock(%s)", (_LOCK_KEY,))
        results: list[list[str]] = []
        waiting = threading.Thread(
            target=lambda: results.append(migrate(DatabaseSettings(DATABASE__URL=dsn), MIGRATIONS))
        )
        waiting.start()
        waiting.join(timeout=1)
        self.assertTrue(waiting.is_alive())
        holder.close()
        waiting.join()
        self.assertEqual(results, [_ids()])

    def test_migration_gives_up_on_a_locked_table(self) -> None:
        dsn = self._empty_database()
        settings = DatabaseSettings(DATABASE__URL=dsn)
        migrate(settings, MIGRATIONS)
        with psycopg.connect(dsn) as traffic:
            traffic.execute("SELECT * FROM payments")
            started = time.monotonic()
            with self.assertRaises(psycopg.errors.LockNotAvailable):
                migrate(settings, [*MIGRATIONS, _LOCK_PROBE])
            self.assertLess(time.monotonic() - started, 30)

    def _empty_database(self) -> str:
        name = f"startup_{uuid.uuid4().hex}"
        with psycopg.connect(self.dsn, autocommit=True) as admin:
            admin.execute(sql.SQL("CREATE DATABASE {}").format(sql.Identifier(name)))
        self.addCleanup(self._drop, name)
        return self.dsn.rsplit("/", 1)[0] + f"/{name}"

    def _drop(self, name: str) -> None:
        with psycopg.connect(self.dsn, autocommit=True) as admin:
            admin.execute(sql.SQL("DROP DATABASE {} WITH (FORCE)").format(sql.Identifier(name)))


def _ids() -> list[str]:
    """``<owner>/<id>`` of every bundled migration in the order they are applied."""
    return [
        f"{migration_set.owner}/{migration.id}"
        for migration_set in MIGRATIONS
        for migration in migration_set.migrations()
    ]


def _applied(conn: psycopg.Connection) -> dict[str, list[tuple[object, ...]]]:
    """Applied migrations and their times by owner, from the yoyo table of each set."""
    return {
        migration_set.owner: conn.execute(
            sql.SQL("SELECT migration_id, applied_at_utc FROM {} ORDER BY migration_id").format(
                sql.Identifier(migration_set.migration_table)
            )
        ).fetchall()
        for migration_set in MIGRATIONS
    }


# A set of the test fixtures that needs an exclusive lock on ``payments``.
_LOCK_PROBE = MigrationSet("lock_probe", "fixtures.lock_probe")


class _Lifecycle:
    def __init__(self, events: list[str], error: Exception | None = None) -> None:
        self._events = events
        self._error = error

    def run(self, worker: object) -> None:
        self._events.append("run")
        if self._error is not None:
            raise self._error


def _resource(events: list[str], name: str, error: Exception | None = None) -> Iterator[str]:
    events.append(f"init {name}")
    if error is not None:
        raise error
    try:
        yield name
    finally:
        events.append(f"shutdown {name}")


class EntrypointResourceOrderTest(unittest.TestCase):
    """Telemetry is initialized first and flushed last; failures keep the exit contract."""

    @override
    def setUp(self) -> None:
        # main() настраивает JSON-логи процесса на root logger; тест возвращает прежние.
        root = logging.getLogger()
        handlers, level = list(root.handlers), root.level

        def restore() -> None:
            for handler in list(root.handlers):
                root.removeHandler(handler)
            for handler in handlers:
                root.addHandler(handler)
            root.setLevel(level)

        self.addCleanup(restore)

    def payments(self, events: list[str], **errors_: Exception) -> PaymentsContainer:
        container = PaymentsContainer()
        container.telemetry.override(TelemetryResource(_resource, events, "telemetry"))
        container.database.override(
            ProcessResource(_resource, events, "database", errors_.get("database"))
        )
        container.spicedb.override(providers.Object(None))
        container.workers.override(providers.Object(object()))
        container.lifecycle.override(providers.Object(_Lifecycle(events, errors_.get("run"))))
        return container

    def run_payments(self, container: PaymentsContainer) -> tuple[int, str]:
        stdout = io.StringIO()
        env = {"DATABASE__URL": "postgresql://payments@127.0.0.1:1/x"}
        with (
            patch.dict(os.environ, env, clear=True),
            patch.object(payments_main, "PaymentsContainer", lambda: container),
            redirect_stdout(stdout),
        ):
            code = payments_main.main()
        return code, stdout.getvalue()

    def test_payments_resources_wrap_the_lifecycle(self) -> None:
        events: list[str] = []
        code, _ = self.run_payments(self.payments(events))
        self.assertEqual(code, 0)
        self.assertEqual(
            events,
            ["init telemetry", "init database", "run", "shutdown database", "shutdown telemetry"],
        )

    def test_unreachable_database_exits_1_and_flushes_telemetry(self) -> None:
        events: list[str] = []
        container = self.payments(events, database=RuntimeError("connection refused"))
        code, stderr = self.run_payments(container)
        self.assertEqual(code, 1)
        self.assertIn("payments: connection refused", stderr)
        self.assertEqual(events, ["init telemetry", "init database", "shutdown telemetry"])

    def test_lifecycle_error_releases_database_before_telemetry(self) -> None:
        events: list[str] = []
        code, stderr = self.run_payments(self.payments(events, run=RuntimeError("drain timeout")))
        self.assertEqual(code, 1)
        self.assertIn("payments: drain timeout", stderr)
        self.assertEqual(events[-2:], ["shutdown database", "shutdown telemetry"])


if __name__ == "__main__":
    unittest.main()
