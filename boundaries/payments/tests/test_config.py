import os
import unittest
from datetime import timedelta
from pathlib import Path
from typing import Any
from unittest.mock import patch

import yaml
from payment_fakes import REQUIRED_SECRETS, REQUIRED_SPEC, write_config

from pkg.config import (
    ConfigError,
    load_admin_settings,
    load_settings,
    parse_document,
    parse_spec,
    settings,
)


def _document(**overrides: object) -> dict[str, object]:
    return {
        "apiVersion": "config.nomnom.dev/v1alpha1",
        "kind": "Payments",
        "metadata": {"name": "payments"},
        "spec": REQUIRED_SPEC,
        **overrides,
    }


class ConfigFileTest(unittest.TestCase):
    """FR-762, FR-766: the versioned file of the process role."""

    def test_loads_the_file_of_its_role(self) -> None:
        # AC-1067
        spec = {**REQUIRED_SPEC, "shutdown": {"drainTimeout": "1m"}}
        environment = {**REQUIRED_SECRETS, "CONFIG_FILE": write_config(self, spec)}
        with patch.dict(os.environ, environment, clear=True):
            loaded = load_settings()
        self.assertEqual(loaded.shutdown.drain_timeout, timedelta(minutes=1))
        self.assertEqual(loaded.event_store.snapshot_every, 5)

    def test_rejects_another_version_or_kind(self) -> None:
        # AC-1068
        for overrides in (
            {"apiVersion": "config.nomnom.dev/v1beta1"},
            {"kind": "Delivery"},
            {"apiVersion": None},
        ):
            with (
                self.subTest(overrides=overrides),
                self.assertRaisesRegex(
                    ConfigError, "apiVersion config.nomnom.dev/v1alpha1 and kind Payments"
                ),
            ):
                parse_document(_document(**overrides), "payments")

    def test_rejects_the_file_of_another_role(self) -> None:
        # AC-1069
        with self.assertRaisesRegex(ConfigError, "metadata.name must be the process role"):
            parse_document(_document(metadata={"name": "payments-worker"}), "payments")

    def test_admin_accepts_the_file_of_any_role(self) -> None:
        # AC-1070
        environment = {
            **REQUIRED_SECRETS,
            "CONFIG_FILE": write_config(self, REQUIRED_SPEC, name="payments-worker"),
        }
        with patch.dict(os.environ, environment, clear=True):
            admin = load_admin_settings()
            with self.assertRaisesRegex(ConfigError, "metadata.name"):
                load_settings()
        self.assertEqual(admin.event_store.snapshot_every, 5)
        self.assertEqual(admin.telemetry.service_name, "payments-admin")

    def test_admin_still_checks_version_and_kind(self) -> None:
        with self.assertRaisesRegex(ConfigError, "kind Payments"):
            parse_document(_document(kind="Orders"), None)

    def test_requires_the_file(self) -> None:
        # AC-1071
        missing = str(Path(write_config(self, REQUIRED_SPEC)).parent / "absent.yaml")
        for environment in ({}, {"CONFIG_FILE": ""}, {"CONFIG_FILE": missing}):
            with (
                self.subTest(environment=environment),
                patch.dict(os.environ, {**REQUIRED_SECRETS, **environment}, clear=True),
                self.assertRaisesRegex(ConfigError, "CONFIG_FILE|cannot read"),
            ):
                load_settings()

    def test_example_file_is_valid(self) -> None:
        example = Path(__file__).resolve().parents[1] / "config.example.yaml"
        spec = parse_document(yaml.safe_load(example.read_text(encoding="utf-8")), "payments")
        self.assertEqual(spec.provider.kind, "fake")

    def test_rejects_a_document_that_is_not_a_mapping(self) -> None:
        for document in (None, [], "payments"):
            with self.subTest(document=document), self.assertRaises(ConfigError):
                parse_document(document, "payments")

    def test_rejects_unknown_top_level_key(self) -> None:
        # AC-1073
        with self.assertRaisesRegex(ConfigError, "status: unknown field"):
            parse_document(_document(status={}), "payments")

    def test_requires_spec(self) -> None:
        document = _document()
        del document["spec"]
        with self.assertRaisesRegex(ConfigError, "spec: Field required"):
            parse_document(document, "payments")


class StrictSpecTest(unittest.TestCase):
    """FR-763: camelCase keys, an unknown key names its path."""

    def test_unknown_key_names_its_path(self) -> None:
        # AC-1072
        with self.assertRaisesRegex(ConfigError, r"spec\.shutdown\.drainTimout: unknown field"):
            parse_spec({**REQUIRED_SPEC, "shutdown": {"drainTimout": "20s"}})

    def test_unknown_section_is_an_error(self) -> None:
        with self.assertRaisesRegex(ConfigError, r"spec\.kafka: unknown field"):
            parse_spec({**REQUIRED_SPEC, "kafka": {}})

    def test_snake_case_key_is_unknown(self) -> None:
        with self.assertRaisesRegex(ConfigError, r"spec\.shutdown\.drain_timeout: unknown field"):
            parse_spec({**REQUIRED_SPEC, "shutdown": {"drain_timeout": "20s"}})

    def test_secret_in_the_file_is_unknown(self) -> None:
        # AC-1075: у секретов нет полей в spec.
        for section, key in (
            ("database", "uri"),
            ("spicedb", "presharedKey"),
            ("provider", "webhookSecret"),
            ("provider", "stripeApiKey"),
            ("eventStore", "subjectKeks"),
            ("eventStore", "subjectRefKey"),
        ):
            spec: dict[str, Any] = {**REQUIRED_SPEC, section: {key: "secret"}}
            if section == "eventStore":
                spec[section]["snapshotEvery"] = 5
            with (
                self.subTest(key=key),
                self.assertRaisesRegex(ConfigError, rf"spec\.{section}\.{key}: unknown field"),
            ):
                parse_spec(spec)

    def test_non_secret_environment_is_ignored(self) -> None:
        # AC-1074
        spec = {**REQUIRED_SPEC, "shutdown": {"drainTimeout": "20s"}}
        environment = {
            **REQUIRED_SECRETS,
            "CONFIG_FILE": write_config(self, spec),
            "SHUTDOWN__DRAIN_TIMEOUT": "0ms",
            "SERVER__PORT": "1",
            "PORT": "2",
            "EVENT_STORE__SNAPSHOT_EVERY": "0",
            "PROVIDER__KIND": "unknown",
            "LOG__LEVEL": "debug",
        }
        with patch.dict(os.environ, environment, clear=True):
            loaded = load_settings()
        self.assertEqual(loaded.shutdown.drain_timeout, timedelta(seconds=20))
        self.assertEqual(loaded.server.port, 8084)
        self.assertEqual(loaded.event_store.snapshot_every, 5)
        self.assertEqual(loaded.provider.kind, "")
        self.assertEqual(loaded.telemetry.log_level, "info")


class SecretsTest(unittest.TestCase):
    """FR-764: secrets only from env; an empty required secret stops the process."""

    def test_requires_database_uri(self) -> None:
        # AC-1076
        for value in (None, "", "   "):
            environment = {k: v for k, v in REQUIRED_SECRETS.items() if k != "DATABASE__URI"}
            if value is not None:
                environment["DATABASE__URI"] = value
            with (
                self.subTest(uri=value),
                patch.dict(os.environ, environment, clear=True),
                self.assertRaisesRegex(ConfigError, "DATABASE__URI"),
            ):
                settings(parse_spec(REQUIRED_SPEC))

    def test_rejects_database_uri_that_is_not_postgresql_uri(self) -> None:
        for value in (
            "host=localhost dbname=payments user=payments",
            "dbname=payments",
            "mysql://payments@localhost/payments",
            "postgresql+psycopg://payments@localhost/payments",
            "localhost:5432/payments",
        ):
            with (
                self.subTest(uri=value),
                patch.dict(os.environ, {**REQUIRED_SECRETS, "DATABASE__URI": value}, clear=True),
                self.assertRaisesRegex(ConfigError, "DATABASE__URI must be a PostgreSQL") as error,
            ):
                settings(parse_spec(REQUIRED_SPEC))
            self.assertNotIn(value, str(error.exception))

    def test_accepts_both_postgresql_uri_schemes(self) -> None:
        for value in (
            "postgresql://payments@localhost/payments",
            "postgres://payments:secret@localhost:5432/payments?sslmode=disable",
        ):
            with (
                self.subTest(uri=value),
                patch.dict(os.environ, {**REQUIRED_SECRETS, "DATABASE__URI": value}, clear=True),
            ):
                self.assertEqual(settings(parse_spec(REQUIRED_SPEC)).database.uri, value)

    def test_subject_keks_are_required_and_valid(self) -> None:
        for value in (None, "", "not json", "{}", '{"1": "short"}', '{"0": "' + "k" * 32 + '"}'):
            environment = {
                k: v for k, v in REQUIRED_SECRETS.items() if k != "EVENT_STORE__SUBJECT_KEKS"
            }
            if value is not None:
                environment["EVENT_STORE__SUBJECT_KEKS"] = value
            with (
                self.subTest(value=value),
                patch.dict(os.environ, environment, clear=True),
                self.assertRaisesRegex(ConfigError, "EVENT_STORE__SUBJECT_KEKS"),
            ):
                settings(parse_spec(REQUIRED_SPEC))

    def test_subject_ref_key_is_required_and_long_enough(self) -> None:
        for value in (None, "short"):
            environment = {
                k: v for k, v in REQUIRED_SECRETS.items() if k != "EVENT_STORE__SUBJECT_REF_KEY"
            }
            if value is not None:
                environment["EVENT_STORE__SUBJECT_REF_KEY"] = value
            with (
                self.subTest(value=value),
                patch.dict(os.environ, environment, clear=True),
                self.assertRaisesRegex(ConfigError, "EVENT_STORE__SUBJECT_REF_KEY"),
            ):
                settings(parse_spec(REQUIRED_SPEC))

    def test_secrets_stay_out_of_repr(self) -> None:
        with patch.dict(os.environ, REQUIRED_SECRETS, clear=True):
            loaded = settings(parse_spec(REQUIRED_SPEC))
        self.assertEqual(set(loaded.event_store.subject_keks), {1})
        self.assertNotIn(loaded.event_store.subject_keks[1], repr(loaded.event_store))
        self.assertNotIn(loaded.event_store.subject_ref_key, repr(loaded.event_store))
        self.assertNotIn("payments@", repr(loaded.database))

    def test_webhook_secret_comes_from_environment(self) -> None:
        environment = {**REQUIRED_SECRETS, "PROVIDER__WEBHOOK_SECRET": " whsec_x "}
        with patch.dict(os.environ, environment, clear=True):
            self.assertEqual(settings(parse_spec(REQUIRED_SPEC)).provider.webhook_secret, "whsec_x")


class EventStoreTest(unittest.TestCase):
    def test_snapshot_every_is_required_and_positive(self) -> None:
        for spec in (
            {},
            {"eventStore": {}},
            {"eventStore": {"snapshotEvery": "abc"}},
            {"eventStore": {"snapshotEvery": 0}},
            {"eventStore": {"snapshotEvery": -1}},
        ):
            with self.subTest(spec=spec), self.assertRaisesRegex(ConfigError, r"spec\.eventStore"):
                parse_spec(spec)


class RuntimeTest(unittest.TestCase):
    def test_reads_management_and_shutdown(self) -> None:
        spec = parse_spec(
            {
                **REQUIRED_SPEC,
                "server": {"port": 9004},
                "management": {"port": 9100},
                "shutdown": {
                    "readinessDelay": "500ms",
                    "drainTimeout": "1m",
                    "telemetryFlushTimeout": "2s",
                },
            }
        )
        self.assertEqual(spec.server.port, 9004)
        self.assertEqual(spec.management.port, 9100)
        self.assertEqual(spec.shutdown.readiness_delay, timedelta(milliseconds=500))
        self.assertEqual(spec.shutdown.drain_timeout, timedelta(minutes=1))
        self.assertEqual(spec.shutdown.telemetry_flush_timeout, timedelta(seconds=2))

    def test_defaults(self) -> None:
        spec = parse_spec(REQUIRED_SPEC)
        self.assertEqual(spec.server.port, 8084)
        self.assertEqual(spec.management.port, 9084)
        self.assertEqual(spec.provider.webhook_port, 8085)
        self.assertEqual(spec.shutdown.readiness_delay, timedelta(seconds=5))
        self.assertEqual(spec.shutdown.drain_timeout, timedelta(seconds=20))

    def test_allows_zero_readiness_delay(self) -> None:
        spec = parse_spec({**REQUIRED_SPEC, "shutdown": {"readinessDelay": "0s"}})
        self.assertEqual(spec.shutdown.readiness_delay, timedelta(0))

    def test_rejects_invalid_ports(self) -> None:
        for section, key in (
            ("server", "port"),
            ("management", "port"),
            ("provider", "webhookPort"),
        ):
            for value in (0, 65536, "abc"):
                with (
                    self.subTest(section=section, value=value),
                    self.assertRaisesRegex(ConfigError, rf"spec\.{section}\.{key}"),
                ):
                    parse_spec({**REQUIRED_SPEC, section: {key: value}})

    def test_rejects_shared_ports(self) -> None:
        for spec, message in (
            ({"server": {"port": 9084}}, "spec.management.port must differ"),
            ({"provider": {"webhookPort": 8084}}, "spec.provider.webhookPort must differ"),
            ({"provider": {"webhookPort": 9084}}, "spec.provider.webhookPort must differ"),
        ):
            with (
                self.subTest(spec=spec),
                patch.dict(os.environ, REQUIRED_SECRETS, clear=True),
                self.assertRaisesRegex(ConfigError, message),
            ):
                settings(parse_spec({**REQUIRED_SPEC, **spec}))

    def test_rejects_invalid_durations(self) -> None:
        for value in ("5", "5h", "1.5s", "-1s", "5 s", "PT5S", "", "s", 5):
            for key in ("readinessDelay", "drainTimeout", "telemetryFlushTimeout"):
                with (
                    self.subTest(key=key, value=value),
                    self.assertRaisesRegex(ConfigError, rf"spec\.shutdown\.{key}"),
                ):
                    parse_spec({**REQUIRED_SPEC, "shutdown": {key: value}})

    def test_rejects_zero_drain_timeout(self) -> None:
        with self.assertRaisesRegex(ConfigError, "drain timeout must be positive"):
            parse_spec({**REQUIRED_SPEC, "shutdown": {"drainTimeout": "0ms"}})


class DatabaseTest(unittest.TestCase):
    def test_reads_pool_settings(self) -> None:
        spec = {**REQUIRED_SPEC, "database": {"maxConns": 4, "connectTimeout": "2500ms"}}
        with patch.dict(os.environ, REQUIRED_SECRETS, clear=True):
            database = settings(parse_spec(spec)).database
        self.assertEqual(database.uri, REQUIRED_SECRETS["DATABASE__URI"])
        self.assertEqual(database.max_conns, 4)
        self.assertEqual(database.connect_timeout, timedelta(milliseconds=2500))

    def test_rejects_invalid_pool_settings(self) -> None:
        for key, value in (
            ("maxConns", 0),
            ("minConns", -1),
            ("minConns", 2),
            ("maxConnLifetime", "0s"),
            ("maxConnIdleTime", "0m"),
            ("connectTimeout", "0ms"),
            ("acquireTimeout", "5"),
        ):
            with (
                self.subTest(key=key, value=value),
                self.assertRaisesRegex(ConfigError, rf"spec\.database\.{key}"),
            ):
                parse_spec({**REQUIRED_SPEC, "database": {key: value}})


class TelemetryConfigTest(unittest.TestCase):
    """FR-765, FR-767, FR-768: telemetry from the file and the pod name."""

    def test_defaults(self) -> None:
        with patch.dict(os.environ, REQUIRED_SECRETS, clear=True):
            loaded = settings(parse_spec(REQUIRED_SPEC)).telemetry
        self.assertEqual(loaded.endpoint, "")
        self.assertEqual(loaded.protocol, "grpc")
        self.assertEqual(loaded.service_name, "payments")
        self.assertEqual(loaded.instance_id, "payments-test")
        self.assertEqual(loaded.resource_attributes, {})
        self.assertFalse(loaded.tracing_enabled)
        self.assertFalse(loaded.metrics_enabled)
        self.assertEqual(loaded.metrics_export_interval, timedelta(seconds=60))
        self.assertEqual(loaded.log_level, "info")

    def test_reads_the_sections(self) -> None:
        spec = {
            **REQUIRED_SPEC,
            "telemetry": {
                "endpoint": "http://alloy:4318",
                "protocol": "http/protobuf",
                "serviceName": "payments-canary",
                "resourceAttributes": {"deployment.environment.name": "kind"},
            },
            "tracing": {"enabled": True},
            "metrics": {"enabled": True, "exportInterval": "10s"},
            "logging": {"level": "warn"},
        }
        with patch.dict(os.environ, REQUIRED_SECRETS, clear=True):
            loaded = settings(parse_spec(spec)).telemetry
        self.assertEqual(loaded.endpoint, "http://alloy:4318")
        self.assertEqual(loaded.protocol, "http/protobuf")
        self.assertEqual(loaded.service_name, "payments-canary")
        self.assertEqual(loaded.resource_attributes, {"deployment.environment.name": "kind"})
        self.assertTrue(loaded.tracing_enabled)
        self.assertTrue(loaded.metrics_enabled)
        self.assertEqual(loaded.metrics_export_interval, timedelta(seconds=10))
        self.assertEqual(loaded.log_level, "warn")

    def test_export_requires_endpoint(self) -> None:
        # AC-1077
        for signal in ("tracing", "metrics"):
            with (
                self.subTest(signal=signal),
                self.assertRaisesRegex(ConfigError, r"spec\.telemetry\.endpoint is required"),
            ):
                parse_spec(
                    {**REQUIRED_SPEC, signal: {"enabled": True}, "telemetry": {"endpoint": " "}}
                )

    def test_endpoint_without_flags_is_accepted(self) -> None:
        # AC-1080: экспорт выключен, проверка в tests/test_telemetry.py.
        spec = parse_spec({**REQUIRED_SPEC, "telemetry": {"endpoint": "http://alloy:4317"}})
        self.assertFalse(spec.tracing.enabled)
        self.assertFalse(spec.metrics.enabled)

    def test_rejects_unknown_protocol(self) -> None:
        # AC-1078
        with self.assertRaisesRegex(ConfigError, r"spec\.telemetry\.protocol") as error:
            parse_spec({**REQUIRED_SPEC, "telemetry": {"protocol": "http/json"}})
        self.assertIn("'grpc'", str(error.exception))
        self.assertIn("'http/protobuf'", str(error.exception))

    def test_rejects_identity_in_resource_attributes(self) -> None:
        # AC-1081
        for key in ("service.name", "service.instance.id"):
            with (
                self.subTest(key=key),
                self.assertRaisesRegex(ConfigError, rf"spec\.telemetry\.resourceAttributes.*{key}"),
            ):
                parse_spec({**REQUIRED_SPEC, "telemetry": {"resourceAttributes": {key: "x"}}})

    def test_requires_pod_name(self) -> None:
        # AC-1082
        for value in (None, "", " "):
            environment = {k: v for k, v in REQUIRED_SECRETS.items() if k != "POD_NAME"}
            if value is not None:
                environment["POD_NAME"] = value
            with (
                self.subTest(value=value),
                patch.dict(os.environ, environment, clear=True),
                self.assertRaisesRegex(ConfigError, "POD_NAME"),
            ):
                settings(parse_spec(REQUIRED_SPEC))

    def test_rejects_invalid_values(self) -> None:
        for spec in (
            {"metrics": {"exportInterval": "0s"}},
            {"metrics": {"exportInterval": "1h"}},
            {"logging": {"level": "trace"}},
            {"telemetry": {"serviceName": ""}},
        ):
            with self.subTest(spec=spec), self.assertRaises(ConfigError):
                parse_spec({**REQUIRED_SPEC, **spec})


if __name__ == "__main__":
    unittest.main()
