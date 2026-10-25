import os
import unittest
from datetime import timedelta
from unittest.mock import patch

from pydantic import ValidationError

from pkg.config import load_settings


class ConfigTest(unittest.TestCase):
    def test_uses_default_port(self) -> None:
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(load_settings().server.port, 8084)

    def test_server_port_takes_precedence_over_port(self) -> None:
        environment = {
            "SERVER__PORT": "9004",
            "PORT": "9005",
        }
        with patch.dict(os.environ, environment, clear=True):
            self.assertEqual(load_settings().server.port, 9004)

    def test_rejects_invalid_port(self) -> None:
        with patch.dict(os.environ, {"PORT": "70000"}, clear=True):
            with self.assertRaises(ValidationError):
                load_settings()

    def test_uses_default_management_and_shutdown(self) -> None:
        with patch.dict(os.environ, {}, clear=True):
            settings = load_settings()
        self.assertEqual(settings.management.port, 9084)
        self.assertEqual(settings.shutdown.readiness_delay, timedelta(seconds=5))
        self.assertEqual(settings.shutdown.drain_timeout, timedelta(seconds=20))

    def test_reads_management_and_shutdown_from_environment(self) -> None:
        environment = {
            "MANAGEMENT__PORT": "9100",
            "SHUTDOWN__READINESS_DELAY": "500ms",
            "SHUTDOWN__DRAIN_TIMEOUT": "1m",
        }
        with patch.dict(os.environ, environment, clear=True):
            settings = load_settings()
        self.assertEqual(settings.management.port, 9100)
        self.assertEqual(settings.shutdown.readiness_delay, timedelta(milliseconds=500))
        self.assertEqual(settings.shutdown.drain_timeout, timedelta(minutes=1))

    def test_allows_zero_readiness_delay(self) -> None:
        with patch.dict(os.environ, {"SHUTDOWN__READINESS_DELAY": "0s"}, clear=True):
            self.assertEqual(load_settings().shutdown.readiness_delay, timedelta(0))

    def test_rejects_invalid_management_port(self) -> None:
        for value in ("0", "65536", "abc"):
            with self.subTest(value=value):
                with patch.dict(os.environ, {"MANAGEMENT__PORT": value}, clear=True):
                    with self.assertRaises(ValidationError):
                        load_settings()

    def test_rejects_management_port_equal_to_server_port(self) -> None:
        environment = {"SERVER__PORT": "9084", "MANAGEMENT__PORT": "9084"}
        with patch.dict(os.environ, environment, clear=True):
            with self.assertRaisesRegex(ValueError, "management port must differ"):
                load_settings()

    def test_rejects_invalid_durations(self) -> None:
        for value in ("5", "5h", "1.5s", "-1s", "5 s", "PT5S", "", "s"):
            for env in ("SHUTDOWN__READINESS_DELAY", "SHUTDOWN__DRAIN_TIMEOUT"):
                with self.subTest(env=env, value=value):
                    with patch.dict(os.environ, {env: value}, clear=True):
                        with self.assertRaises(ValidationError):
                            load_settings()

    def test_rejects_zero_drain_timeout(self) -> None:
        with patch.dict(os.environ, {"SHUTDOWN__DRAIN_TIMEOUT": "0s"}, clear=True):
            with self.assertRaises(ValidationError):
                load_settings()
