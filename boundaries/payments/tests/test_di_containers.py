import http.client
import io
import json
import os
import threading
import time
import unittest
from collections.abc import Iterator
from contextlib import redirect_stderr
from typing import Any
from unittest.mock import patch

from dependency_injector import errors, providers
from payment_fakes import (
    NOW,
    PaymentsTestKit,
    close_container,
    payments_container,
    unreachable_database,
)

from di.migrate import MigrateContainer
from di.payments import PaymentsContainer, SpiceDBConfigurationError
from di.relay import RelayContainer
from di.resources import ProcessResource, TelemetryResource, sections
from entrypoints.payments import main as payments_main
from entrypoints.payments_relay import main as relay_main
from infrastructure.authorization import SpiceDBPermissionChecker
from infrastructure.grpc.service import GrpcWorker, PaymentsHandlers
from infrastructure.messaging import OutboxRelay, PaymentEventsMapper, RelayWorker
from infrastructure.observability import ObservedOutbox, ObservedUnitOfWork
from infrastructure.postgres import PostgresPaymentOutbox
from infrastructure.provider import (
    SIGNATURE_HEADER,
    WEBHOOK_PATH,
    FakePaymentProvider,
    sign_payload,
)
from pkg.application import Subject, SubjectKind
from pkg.config import DatabaseSettings, load_relay_settings
from pkg.lifecycle import HttpWorker, WorkerGroup
from slices.payment.applications.create_payment import CreatePaymentInput
from slices.payment.domains.payment import OperationKind, OperationState, PaymentState

SPICEDB_ENV = {
    "SPICEDB__ENDPOINT": "127.0.0.1:1",
    "SPICEDB__PRESHARED_KEY": "devkey",
    "SPICEDB__INSECURE": "true",
}
SECRET = "whsec_test"
WORKER = Subject(SubjectKind.SERVICE, "orders-worker")
RELAY_ENV = {
    "DATABASE__URL": "postgresql://payments@127.0.0.1:1/x",
    "KAFKA__BROKERS": "127.0.0.1:1",
}


def _shutdown_workers(container: PaymentsContainer) -> None:
    container.grpc_worker().shutdown(time.monotonic() + 1)


class PaymentsContainerTest(unittest.TestCase):
    def container(self, **environment: str) -> PaymentsContainer:
        container = payments_container({**SPICEDB_ENV, **environment})
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
        self.assertIsInstance(container.provider(), FakePaymentProvider)
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
        container = payments_container()
        self.addCleanup(close_container, container)
        with self.assertRaisesRegex(SpiceDBConfigurationError, "SPICEDB__ENDPOINT is required"):
            container.workers()

    def test_override_substitutes_fakes_in_use_cases(self) -> None:
        kit = PaymentsTestKit()
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
        self.assertEqual(kit.store.payments[result.payment_id].state, PaymentState.PENDING)
        self.assertEqual(len(kit.permissions.checks), 1)

    def test_webhook_worker_applies_callbacks_through_overridden_storage(self) -> None:
        kit = PaymentsTestKit()
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


class RelayContainerTest(unittest.TestCase):
    def container(self) -> RelayContainer:
        with patch.dict(os.environ, RELAY_ENV, clear=True):
            settings = load_relay_settings()
        container = RelayContainer()
        container.config.from_dict(sections(settings))
        database = unreachable_database()
        self.addCleanup(database.close)
        container.database.override(providers.Object(database))
        return container

    def test_resolves_the_process_graph_with_overridden_publisher(self) -> None:
        container = self.container()
        publisher = _RecordingPublisher()
        container.publisher.override(providers.Object(publisher))

        worker = container.worker()

        self.assertIsInstance(worker, RelayWorker)
        self.assertIsInstance(container.relay(), OutboxRelay)
        self.assertIs(container.worker(), worker)
        self.assertIs(container.publisher(), publisher)

    def test_config_is_strict(self) -> None:
        with self.assertRaises(errors.Error):
            RelayContainer().worker()


class MigrateContainerTest(unittest.TestCase):
    def test_config_is_strict(self) -> None:
        with self.assertRaises(errors.Error):
            MigrateContainer().migrations()

    @unittest.skipUnless(os.environ.get("DATABASE_URL", "").strip(), "DATABASE_URL is not set")
    def test_applies_migrations(self) -> None:
        container = MigrateContainer()
        container.config.from_dict(
            {"database": DatabaseSettings(DATABASE__URL=os.environ["DATABASE_URL"].strip())}
        )
        self.assertIsInstance(container.migrations(), list)


class _RecordingPublisher:
    def publish(self, record: Any) -> None:
        raise AssertionError("not expected")

    def close(self, timeout: float) -> None:
        pass


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
        stderr = io.StringIO()
        env = {"DATABASE__URL": "postgresql://payments@127.0.0.1:1/x"}
        with (
            patch.dict(os.environ, env, clear=True),
            patch.object(payments_main, "PaymentsContainer", lambda: container),
            redirect_stderr(stderr),
        ):
            code = payments_main.main()
        return code, stderr.getvalue()

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

    def test_relay_opens_the_producer_after_the_database(self) -> None:
        events: list[str] = []
        container = RelayContainer()
        container.telemetry.override(TelemetryResource(_resource, events, "telemetry"))
        container.database.override(ProcessResource(_resource, events, "database"))
        container.lifecycle.override(providers.Object(_Lifecycle(events)))
        container.worker.override(providers.Object(object()))

        class Publisher:
            def __init__(self, brokers: str, client_id: str) -> None:
                events.append(f"init publisher {brokers} {client_id}")

        with (
            patch.dict(os.environ, RELAY_ENV, clear=True),
            patch.object(relay_main, "RelayContainer", lambda: container),
            patch("di.relay.KafkaPublisher", Publisher),
        ):
            code = relay_main.main()

        self.assertEqual(code, 0)
        self.assertEqual(
            events,
            [
                "init telemetry",
                "init database",
                "init publisher 127.0.0.1:1 payments-relay",
                "run",
                "shutdown database",
                "shutdown telemetry",
            ],
        )


if __name__ == "__main__":
    unittest.main()
