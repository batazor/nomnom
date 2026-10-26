import os
import unittest
from datetime import timedelta
from unittest.mock import patch

from pydantic import ValidationError

from pkg.config import load_settings

# DATABASE__URL обязателен: без него load_settings падает.
REQUIRED_ENV = {"DATABASE__URL": "postgresql://payments@localhost/payments"}


class ConfigTest(unittest.TestCase):
    def test_server_port_takes_precedence_over_port(self) -> None:
        environment = {
            "SERVER__PORT": "9004",
            "PORT": "9005",
        }
        with patch.dict(os.environ, {**REQUIRED_ENV, **environment}, clear=True):
            self.assertEqual(load_settings().server.port, 9004)

    def test_reads_management_and_shutdown_from_environment(self) -> None:
        environment = {
            "MANAGEMENT__PORT": "9100",
            "SHUTDOWN__READINESS_DELAY": "500ms",
            "SHUTDOWN__DRAIN_TIMEOUT": "1m",
        }
        with patch.dict(os.environ, {**REQUIRED_ENV, **environment}, clear=True):
            settings = load_settings()
        self.assertEqual(settings.management.port, 9100)
        self.assertEqual(settings.shutdown.readiness_delay, timedelta(milliseconds=500))
        self.assertEqual(settings.shutdown.drain_timeout, timedelta(minutes=1))

    def test_allows_zero_readiness_delay(self) -> None:
        with patch.dict(
            os.environ, {**REQUIRED_ENV, "SHUTDOWN__READINESS_DELAY": "0s"}, clear=True
        ):
            self.assertEqual(load_settings().shutdown.readiness_delay, timedelta(0))

    def test_rejects_invalid_management_port(self) -> None:
        for value in ("0", "65536", "abc"):
            with (
                self.subTest(value=value),
                patch.dict(os.environ, {**REQUIRED_ENV, "MANAGEMENT__PORT": value}, clear=True),
                self.assertRaises(ValidationError),
            ):
                load_settings()

    def test_rejects_management_port_equal_to_server_port(self) -> None:
        environment = {"SERVER__PORT": "9084", "MANAGEMENT__PORT": "9084"}
        with (
            patch.dict(os.environ, {**REQUIRED_ENV, **environment}, clear=True),
            self.assertRaisesRegex(ValueError, "management port must differ"),
        ):
            load_settings()

    def test_rejects_invalid_durations(self) -> None:
        for value in ("5", "5h", "1.5s", "-1s", "5 s", "PT5S", "", "s"):
            for env in ("SHUTDOWN__READINESS_DELAY", "SHUTDOWN__DRAIN_TIMEOUT"):
                with (
                    self.subTest(env=env, value=value),
                    patch.dict(os.environ, {**REQUIRED_ENV, env: value}, clear=True),
                    self.assertRaises(ValidationError),
                ):
                    load_settings()

    def test_rejects_zero_drain_timeout(self) -> None:
        with (
            patch.dict(os.environ, {**REQUIRED_ENV, "SHUTDOWN__DRAIN_TIMEOUT": "0s"}, clear=True),
            self.assertRaises(ValidationError),
        ):
            load_settings()

    def test_requires_database_url(self) -> None:
        for value in (None, "", "   "):
            environment = {} if value is None else {"DATABASE__URL": value}
            with (
                self.subTest(url=value),
                patch.dict(os.environ, environment, clear=True),
                self.assertRaisesRegex(ValidationError, "DATABASE__URL"),
            ):
                load_settings()

    def test_reads_database_settings(self) -> None:
        environment = {**REQUIRED_ENV, "DATABASE__MAX_CONNS": "4"}
        with patch.dict(os.environ, environment, clear=True):
            settings = load_settings().database
        self.assertEqual(settings.url, REQUIRED_ENV["DATABASE__URL"])
        self.assertEqual(settings.max_conns, 4)
        self.assertNotIn("payments@localhost", repr(settings))

    def test_rejects_invalid_database_pool_settings(self) -> None:
        for name, value in (
            ("DATABASE__MAX_CONNS", "0"),
            ("DATABASE__MIN_CONNS", "-1"),
            ("DATABASE__MIN_CONNS", "2"),
            ("DATABASE__MAX_CONN_LIFETIME", "0s"),
            ("DATABASE__MAX_CONN_IDLE_TIME", "0m"),
            ("DATABASE__CONNECT_TIMEOUT", "0ms"),
            ("DATABASE__ACQUIRE_TIMEOUT", "5"),
        ):
            with (
                self.subTest(name=name, value=value),
                patch.dict(os.environ, {**REQUIRED_ENV, name: value}, clear=True),
                self.assertRaisesRegex(ValidationError, name),
            ):
                load_settings()
