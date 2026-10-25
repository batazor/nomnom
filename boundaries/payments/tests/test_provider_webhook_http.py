import http.client
import json
import threading
import time
import unittest
from datetime import UTC, datetime, timedelta
from typing import override

import pytest
from payment_fakes import PaymentsTestKit

from infrastructure.postgres import PostgresDatabase
from infrastructure.provider import (
    MAX_BODY_BYTES,
    SIGNATURE_HEADER,
    WEBHOOK_PATH,
    WebhookVerifier,
    create_webhook_handler,
    sign_payload,
)
from pkg.lifecycle import HttpWorker, LifecycleError, WorkerGroup
from slices.payment.applications.apply_provider_callback import ApplyProviderCallbackHandler
from slices.payment.domains.payment import OperationKind, OperationState, PaymentState

NOW = datetime(2026, 10, 25, 12, 0, tzinfo=UTC)
SECRET = "whsec_test"


@pytest.mark.usefixtures("postgres")
class ProviderWebhookHttpTest(unittest.TestCase):
    database: PostgresDatabase

    @override
    def setUp(self) -> None:
        self.kit = PaymentsTestKit(self.database)
        payment_id = self.kit.given_payment(PaymentState.AUTHORIZED)
        self.operation_id = self.kit.given_operation(
            payment_id, OperationKind.CAPTURE, "order-1:capture"
        )
        callbacks = ApplyProviderCallbackHandler(self.kit.uow, self.kit.inbox, self.kit.applier())
        handler = create_webhook_handler(
            WebhookVerifier(SECRET, timedelta(minutes=5)), callbacks, lambda: NOW
        )
        self.worker = HttpWorker(("127.0.0.1", 0), handler, name="webhooks")
        self.worker.start()
        self.thread = threading.Thread(target=self.worker.run, daemon=True)
        self.thread.start()

    @override
    def tearDown(self) -> None:
        self.worker.shutdown(time.monotonic() + 2)
        self.thread.join(2)

    def body(self, message_id: str = "msg-1", operation_id: str = "") -> bytes:
        return json.dumps(
            {
                "id": message_id,
                "operation_id": operation_id or str(self.operation_id),
                "status": "succeeded",
                "provider_reference": "capture-ref",
            }
        ).encode()

    def post(self, body: bytes, signature: str | None = None, path: str = WEBHOOK_PATH):
        if signature is None:
            signature = sign_payload(SECRET, int(NOW.timestamp()), body)
        connection = http.client.HTTPConnection("127.0.0.1", self.worker.port, timeout=5)
        try:
            connection.request("POST", path, body, {SIGNATURE_HEADER: signature})
            response = connection.getresponse()
            return response.status, json.loads(response.read())
        finally:
            connection.close()

    def test_signed_webhook_is_applied_once(self) -> None:
        status, payload = self.post(self.body())
        self.assertEqual((status, payload["status"]), (200, "applied"))
        self.assertEqual(self.kit.operation(self.operation_id).state, OperationState.SUCCEEDED)

        status, payload = self.post(self.body())
        self.assertEqual((status, payload["status"]), (200, "duplicate_message"))

    def test_bad_signature_is_400_and_not_applied(self) -> None:
        status, _ = self.post(self.body(), signature=sign_payload("other", 1, b""))
        self.assertEqual(status, 400)
        self.assertEqual(self.kit.operation(self.operation_id).state, OperationState.PENDING)

    def test_malformed_body_is_400(self) -> None:
        self.assertEqual(self.post(b"{}")[0], 400)

    def test_unknown_operation_is_404(self) -> None:
        self.assertEqual(self.post(self.body(operation_id="missing"))[0], 404)

    def test_other_path_is_404(self) -> None:
        self.assertEqual(self.post(self.body(), path="/other")[0], 404)

    def test_oversized_body_is_413(self) -> None:
        self.assertEqual(self.post(b"x" * (MAX_BODY_BYTES + 1))[0], 413)


class _Worker:
    def __init__(self, fail_start: bool = False) -> None:
        self.fail_start = fail_start
        self.stopped = threading.Event()
        self.shutdowns = 0

    def start(self) -> None:
        if self.fail_start:
            raise LifecycleError("start failed")

    def run(self) -> None:
        self.stopped.wait()

    def shutdown(self, deadline: float) -> None:
        self.shutdowns += 1
        self.stopped.set()


class WorkerGroupTest(unittest.TestCase):
    def test_run_returns_when_any_worker_exits_and_shutdown_stops_all(self) -> None:
        first, second = _Worker(), _Worker()
        group = WorkerGroup(first, second)
        group.start()
        runner = threading.Thread(target=group.run, daemon=True)
        runner.start()
        second.stopped.set()
        runner.join(2)
        self.assertFalse(runner.is_alive())
        group.shutdown(time.monotonic() + 1)
        self.assertEqual((first.shutdowns, second.shutdowns), (1, 1))

    def test_failed_start_shuts_down_already_started_workers(self) -> None:
        first = _Worker()
        with self.assertRaises(LifecycleError):
            WorkerGroup(first, _Worker(fail_start=True)).start()
        self.assertEqual(first.shutdowns, 1)


if __name__ == "__main__":
    unittest.main()
