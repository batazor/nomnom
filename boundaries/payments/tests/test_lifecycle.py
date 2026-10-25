import http.client
import json
import threading
import time
import unittest
from collections.abc import Callable
from http.server import BaseHTTPRequestHandler
from unittest.mock import ANY

from pkg.lifecycle import (
    DrainTimeoutError,
    HttpWorker,
    Lifecycle,
    LifecycleConfig,
    LifecycleError,
    Worker,
)

TIMEOUT = 5.0


def config(readiness_delay: float = 0.05, drain_timeout: float = 0.2) -> LifecycleConfig:
    return LifecycleConfig(
        management_port=0,
        readiness_delay=readiness_delay,
        drain_timeout=drain_timeout,
        management_host="127.0.0.1",
    )


def eventually(condition: Callable[[], bool], timeout: float = TIMEOUT) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if condition():
            return True
        time.sleep(0.005)
    return condition()


class FakeWorker:
    """Обслуживает работу до shutdown; drain задаёт длительность завершения начатой работы."""

    def __init__(self, start_error: Exception | None = None, drain: float = 0.0) -> None:
        self.start_error = start_error
        self.drain = drain
        self.started = threading.Event()
        self.shutdown_at: float | None = None
        self._stop = threading.Event()
        self._exit_error: Exception | None = None

    def start(self) -> None:
        if self.start_error is not None:
            raise self.start_error
        self.started.set()

    def run(self) -> None:
        self._stop.wait()
        if self._exit_error is not None:
            raise self._exit_error

    def shutdown(self, deadline: float) -> None:
        self.shutdown_at = time.monotonic()
        try:
            remaining = deadline - time.monotonic()
            if self.drain > remaining:
                time.sleep(max(0.0, remaining))
                raise DrainTimeoutError("drain timeout exceeded")
            time.sleep(self.drain)
        finally:
            self._stop.set()

    def exit(self, error: Exception | None = None) -> None:
        self._exit_error = error
        self._stop.set()


class LifecycleRun:
    """Запускает Lifecycle.run в отдельном потоке и сохраняет результат."""

    def __init__(self, lifecycle: Lifecycle, worker: Worker) -> None:
        self.stop = threading.Event()
        self.error: BaseException | None = None
        self.stopped_at: float | None = None
        self._thread = threading.Thread(target=self._run, args=(lifecycle, worker), daemon=True)
        self._thread.start()

    def _run(self, lifecycle: Lifecycle, worker: Worker) -> None:
        try:
            lifecycle.run(worker, self.stop)
        except BaseException as error:
            self.error = error

    def request_stop(self) -> None:
        self.stopped_at = time.monotonic()
        self.stop.set()

    def wait(self) -> BaseException | None:
        self._thread.join(TIMEOUT)
        if self._thread.is_alive():
            raise AssertionError("lifecycle did not return")
        return self.error


def get(port: int, path: str) -> tuple[int, dict[str, str], dict[str, object]]:
    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=TIMEOUT)
    try:
        connection.request("GET", path)
        response = connection.getresponse()
        body = json.loads(response.read())
        headers = {name.lower(): value for name, value in response.getheaders()}
        return response.status, headers, body
    finally:
        connection.close()


class LifecycleTest(unittest.TestCase):
    def test_ready_after_start_and_graceful_shutdown(self) -> None:
        lifecycle = Lifecycle(config())
        worker = FakeWorker()
        run = LifecycleRun(lifecycle, worker)

        self.assertTrue(worker.started.wait(TIMEOUT))
        self.assertTrue(eventually(lambda: lifecycle.health.ready))

        run.request_stop()
        self.assertTrue(eventually(lambda: not lifecycle.health.ready))
        self.assertIsNone(run.wait())

    def test_waits_readiness_delay_before_shutdown(self) -> None:
        lifecycle = Lifecycle(config(readiness_delay=0.15))
        worker = FakeWorker()
        run = LifecycleRun(lifecycle, worker)
        self.assertTrue(eventually(lambda: lifecycle.health.ready))

        run.request_stop()
        self.assertIsNone(run.wait())
        assert worker.shutdown_at is not None and run.stopped_at is not None
        self.assertGreaterEqual(worker.shutdown_at - run.stopped_at, 0.15)

    def test_fails_when_drain_timeout_exceeded(self) -> None:
        lifecycle = Lifecycle(config(drain_timeout=0.05))
        worker = FakeWorker(drain=1.0)
        run = LifecycleRun(lifecycle, worker)
        self.assertTrue(eventually(lambda: lifecycle.health.ready))

        run.request_stop()
        error = run.wait()
        self.assertIsInstance(error, LifecycleError)
        self.assertIsInstance(error.__cause__ if error else None, DrainTimeoutError)

    def test_start_failure_aborts_and_never_ready(self) -> None:
        lifecycle = Lifecycle(config())
        worker = FakeWorker(start_error=OSError("address in use"))
        run = LifecycleRun(lifecycle, worker)

        error = run.wait()
        self.assertIsInstance(error, LifecycleError)
        self.assertIn("start", str(error))
        self.assertFalse(lifecycle.health.started)
        self.assertFalse(lifecycle.health.ready)

    def test_worker_stopping_on_its_own_is_error(self) -> None:
        lifecycle = Lifecycle(config())
        worker = FakeWorker()
        run = LifecycleRun(lifecycle, worker)
        self.assertTrue(eventually(lambda: lifecycle.health.ready))

        worker.exit()
        error = run.wait()
        self.assertIsInstance(error, LifecycleError)
        self.assertIn("worker stopped before shutdown", str(error))
        self.assertFalse(lifecycle.health.ready)

    def test_worker_failing_during_readiness_delay_is_error(self) -> None:
        lifecycle = Lifecycle(config(readiness_delay=TIMEOUT))
        worker = FakeWorker()
        run = LifecycleRun(lifecycle, worker)
        self.assertTrue(eventually(lambda: lifecycle.health.ready))

        run.request_stop()
        self.assertTrue(eventually(lambda: lifecycle.health.shutting_down))
        worker.exit(RuntimeError("boom"))
        error = run.wait()
        self.assertIsInstance(error, LifecycleError)
        self.assertIn("boom", str(error))

    def test_health_endpoints(self) -> None:
        lifecycle = Lifecycle(config(readiness_delay=TIMEOUT))
        worker = FakeWorker()
        run = LifecycleRun(lifecycle, worker)
        self.assertTrue(eventually(lambda: lifecycle.health.ready))
        port = lifecycle.management_port
        assert port is not None

        for path in ("/health/startup", "/health/live", "/health/ready"):
            with self.subTest(path=path):
                status, headers, body = get(port, path)
                self.assertEqual(status, 200)
                self.assertEqual(body, {"status": "ok"})
                self.assertEqual(headers["content-type"], "application/json")
                self.assertEqual(headers["cache-control"], "no-store")

        run.request_stop()
        self.assertTrue(eventually(lambda: lifecycle.health.shutting_down))
        self.assertEqual(get(port, "/health/live"), (200, ANY, {"status": "ok"}))
        status, _, body = get(port, "/health/ready")
        self.assertEqual(status, 503)
        self.assertEqual(body, {"status": "unavailable", "reason": "shutting_down"})

        worker.exit()
        run.wait()

    def test_health_reports_initializing_before_start(self) -> None:
        lifecycle = Lifecycle(config())
        release = threading.Event()

        class SlowStartWorker(FakeWorker):
            def start(self) -> None:
                release.wait(TIMEOUT)
                super().start()

        worker = SlowStartWorker()
        run = LifecycleRun(lifecycle, worker)
        self.assertTrue(eventually(lambda: lifecycle.management_port is not None))
        port = lifecycle.management_port
        assert port is not None

        for path in ("/health/startup", "/health/ready"):
            with self.subTest(path=path):
                status, _, body = get(port, path)
                self.assertEqual(status, 503)
                self.assertEqual(body, {"status": "unavailable", "reason": "initializing"})
        self.assertEqual(get(port, "/health/live")[0], 200)

        release.set()
        self.assertTrue(eventually(lambda: lifecycle.health.ready))
        run.request_stop()
        self.assertIsNone(run.wait())


class SlowHandler(BaseHTTPRequestHandler):
    delay = 0.3
    received = threading.Event()

    def do_GET(self) -> None:
        type(self).received.set()
        time.sleep(type(self).delay)
        body = b"done"
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format: str, *args: object) -> None:
        return


class HttpWorkerTest(unittest.TestCase):
    def setUp(self) -> None:
        SlowHandler.received = threading.Event()
        self.worker = HttpWorker(("127.0.0.1", 0), SlowHandler)
        self.worker.start()
        self.run_thread = threading.Thread(target=self.worker.run, daemon=True)
        self.run_thread.start()

    def _slow_request(self, result: dict[str, object]) -> threading.Thread:
        def request() -> None:
            connection = http.client.HTTPConnection("127.0.0.1", self.worker.port, timeout=TIMEOUT)
            try:
                connection.request("GET", "/")
                response = connection.getresponse()
                result["status"] = response.status
                result["body"] = response.read()
            except Exception as error:
                result["error"] = error
            finally:
                connection.close()

        thread = threading.Thread(target=request, daemon=True)
        thread.start()
        self.assertTrue(SlowHandler.received.wait(TIMEOUT))
        return thread

    def test_drains_in_flight_request_within_deadline(self) -> None:
        result: dict[str, object] = {}
        client = self._slow_request(result)

        self.worker.shutdown(time.monotonic() + 2.0)
        self.run_thread.join(TIMEOUT)
        client.join(TIMEOUT)

        self.assertFalse(self.run_thread.is_alive())
        self.assertEqual(result.get("status"), 200)
        self.assertEqual(result.get("body"), b"done")
        with self.assertRaises(OSError):
            http.client.HTTPConnection("127.0.0.1", self.worker.port, timeout=1).request("GET", "/")

    def test_fails_when_deadline_expires(self) -> None:
        result: dict[str, object] = {}
        client = self._slow_request(result)

        with self.assertRaises(DrainTimeoutError):
            self.worker.shutdown(time.monotonic() + 0.05)
        self.run_thread.join(TIMEOUT)
        client.join(TIMEOUT)
        self.assertFalse(self.run_thread.is_alive())


class KeepAliveHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self) -> None:
        self.send_response(200)
        self.send_header("Content-Length", "2")
        self.end_headers()
        self.wfile.write(b"ok")

    def log_message(self, format: str, *args: object) -> None:
        return


class HttpWorkerKeepAliveTest(unittest.TestCase):
    def test_idle_keep_alive_connection_does_not_block_drain(self) -> None:
        worker = HttpWorker(("127.0.0.1", 0), KeepAliveHandler)
        worker.start()
        run_thread = threading.Thread(target=worker.run, daemon=True)
        run_thread.start()

        connection = http.client.HTTPConnection("127.0.0.1", worker.port, timeout=TIMEOUT)
        try:
            connection.request("GET", "/")
            self.assertEqual(connection.getresponse().read(), b"ok")

            # Соединение остаётся открытым и простаивает в ожидании следующего запроса.
            worker.shutdown(time.monotonic() + 2.0)
            run_thread.join(TIMEOUT)
            self.assertFalse(run_thread.is_alive())
        finally:
            connection.close()


if __name__ == "__main__":
    unittest.main()
