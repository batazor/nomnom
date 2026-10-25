import threading
import time
import unittest

from pkg import telemetry


class TelemetryTest(unittest.TestCase):
    def test_disabled_without_endpoint(self) -> None:
        providers = telemetry.setup(environ={})
        self.assertFalse(providers.enabled)
        self.assertTrue(providers.shutdown(0.01))

    def test_shutdown_runs_all_providers(self) -> None:
        calls: list[str] = []

        def failing() -> None:
            calls.append("traces")
            raise RuntimeError("collector unavailable")

        providers = telemetry.Telemetry([failing, lambda: calls.append("metrics")])
        self.assertTrue(providers.shutdown(1.0))
        self.assertEqual(calls, ["traces", "metrics"])

    def test_shutdown_bounded_by_timeout(self) -> None:
        release = threading.Event()
        providers = telemetry.Telemetry([lambda: release.wait(5)])
        started = time.monotonic()
        try:
            self.assertFalse(providers.shutdown(0.1))
            self.assertLess(time.monotonic() - started, 1.0)
        finally:
            release.set()

    def test_setup_with_endpoint_registers_providers(self) -> None:
        providers = telemetry.setup(environ={"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:1"})
        self.assertTrue(providers.enabled)
        self.assertTrue(providers.shutdown(5.0))


if __name__ == "__main__":
    unittest.main()
