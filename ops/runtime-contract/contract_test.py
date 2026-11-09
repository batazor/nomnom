"""Black-box проверка runtime contract (docs/cloud-native-runtime.md).

Один набор сценариев запускается против реального процесса каждого backend-приложения:
startup/live/ready на техническом порту, SIGTERM/SIGINT, прекращение приёма запросов,
exit code и fail fast. Процесс видится только снаружи: через файл конфигурации
(`CONFIG_FILE`), секреты в environment, порты, сигналы и код выхода.

    python3 ops/runtime-contract/contract_test.py              # все приложения
    python3 ops/runtime-contract/contract_test.py orders delivery

Перед прогоном приложения собираются инкрементально (go build, cargo build, uv sync).
"""

from __future__ import annotations

import http.client
import json
import os
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import unittest
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable

ROOT = Path(__file__).resolve().parents[2]
BOUNDARIES = ROOT / "boundaries"
IDENTITY_FIXTURES = ROOT / "tests" / "identity" / "fixtures"
BUILD_DIR = Path(tempfile.mkdtemp(prefix="nomnom-runtime-contract-"))

READINESS_DELAY_S = 1.0
DRAIN_TIMEOUT_S = 5.0
STARTUP_TIMEOUT_S = 30.0
# Запас сверх readiness delay + drain на flush, закрытие портов и выход процесса.
EXIT_MARGIN_S = 3.0


API_VERSION = "config.nomnom.dev/v1alpha1"


@dataclass(frozen=True)
class Service:
    name: str
    cwd: Path
    build: Callable[[], None]
    command: Callable[[], list[str]]
    business_protocol: str
    # Файл конфигурации процесса (ADR 17): kind сервиса, роль и spec без портов и
    # shutdown — их задаёт ServiceProcess.
    kind: str
    spec: dict[str, Any]
    # Секреты передаются только через environment.
    secrets: dict[str, str]


def free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def run_build(cwd: Path, *argv: str) -> None:
    subprocess.run(argv, cwd=cwd, check=True, stdout=subprocess.DEVNULL)


def orders() -> Service:
    cwd = BOUNDARIES / "orders"
    binary = BUILD_DIR / "orders"
    return Service(
        "orders",
        cwd,
        lambda: run_build(cwd, "go", "build", "-o", str(binary), "./cmd/orders"),
        lambda: [str(binary)],
        "grpc",
        "Orders",
        {
            "cancellation": {"window": "5m", "scheduledCutoff": "60m"},
            "scheduledOrder": {"preparationBuffer": "5m", "replanLead": "30m"},
            "internalGateway": {"address": "127.0.0.1:8080"},
            # SpiceDB обязателен (ADR 18); клиенты подключаются лениво, поэтому
            # контракт lifecycle проверяется без запущенного SpiceDB.
            "spicedb": {"endpoint": "127.0.0.1:50051", "insecure": True},
            "simulator": {
                "interval": "5s",
                "minDelay": "30s",
                "maxDelay": "2m",
                "customerId": "runtime-contract",
                "marketIds": "berlin",
            },
        },
        {
            "SPICEDB__PRESHARED_KEY": "runtime-contract",
            # PostgreSQL обязателен для orders (ADR 8); pool подключается лениво.
            "POSTGRES__DSN": "postgres://runtime:contract@127.0.0.1:1/orders?sslmode=disable",
        },
    )


def restaurants() -> Service:
    cwd = BOUNDARIES / "restaurants"
    return Service(
        "restaurants",
        cwd,
        lambda: None if (cwd / "node_modules").exists() else run_build(cwd, "npm", "ci"),
        lambda: ["node", "cmd/restaurants/main.ts"],
        "grpc",
        "Restaurants",
        {
            "identity": {
                "issuer": "https://kubernetes.default.svc.cluster.local",
                "audience": "nomnom.restaurants",
                "jwksFile": str(IDENTITY_FIXTURES / "jwks.json"),
                "serviceAccounts": "nomnom-orders/orders=orders",
                "userIssuer": "nomnom-bff",
                "userJwksFile": str(IDENTITY_FIXTURES / "bff-jwks.json"),
                "userDelegates": "bff",
            },
            "spicedb": {"endpoint": "127.0.0.1:50051", "insecure": True},
            "simulator": {"interval": "5s", "minDelay": "30s", "maxDelay": "2m"},
            "scheduledSlot": {
                "width": "15m",
                "minLead": "45m",
                "maxHorizon": "48h",
                "holdTtl": "10m",
                "alternatives": 3,
            },
        },
        {
            "SPICEDB__PRESHARED_KEY": "runtime-contract",
            "MONGODB__URI": "mongodb://127.0.0.1:1/restaurants",
            "VALKEY__URL": "redis://127.0.0.1:1",
        },
    )


def delivery() -> Service:
    cwd = BOUNDARIES / "delivery"
    return Service(
        "delivery",
        cwd,
        lambda: run_build(cwd, "cargo", "build", "--quiet", "--bin", "delivery"),
        lambda: [str(cwd / "target" / "debug" / "delivery")],
        "grpc",
        "Delivery",
        {
            "identity": {
                "issuer": "https://kubernetes.default.svc.cluster.local",
                "audience": "nomnom.delivery",
                "jwksFile": str(IDENTITY_FIXTURES / "jwks.json"),
                "serviceAccounts": "nomnom-orders/orders-worker=orders-worker",
                "userIssuer": "nomnom-bff",
                "userJwksFile": str(IDENTITY_FIXTURES / "bff-jwks.json"),
                "userDelegates": "bff",
            },
            "spicedb": {"endpoint": "127.0.0.1:50051", "insecure": True},
            "temporal": {
                "hostPort": "127.0.0.1:7233",
                "namespace": "default",
                "connectTimeout": "10s",
            },
            "simulator": {
                "interval": "5s",
                "minDelay": "30s",
                "maxDelay": "2m",
                "trackInterval": "1s",
            },
            "tracking": {
                "cityOriginLatitude": 52.515,
                "cityOriginLongitude": 13.385,
                "cityBlocks": 12,
                "blockMeters": 150.0,
                "persistInterval": "15s",
            },
            "kafka": {
                "brokers": "127.0.0.1:9092",
                "consumerGroup": "delivery",
                "initialOffset": "oldest",
            },
            "schemaRegistry": {"url": "http://127.0.0.1:8081"},
            "deliveryEstimate": {
                "sampleSize": 50,
                "baselineCourierLead": "15m",
                "baselineTransit": "20m",
            },
        },
        {
            "SPICEDB__PRESHARED_KEY": "runtime-contract",
            "DATABASE__URI": "postgres://runtime:contract@127.0.0.1:1/delivery",
            "VALKEY__URL": "redis://127.0.0.1:1",
        },
    )


def payments() -> Service:
    cwd = BOUNDARIES / "payments"
    # Сигнал должен получать сам интерпретатор, поэтому процесс запускается без `uv run`.
    return Service(
        "payments",
        cwd,
        lambda: run_build(cwd, "uv", "sync", "--frozen", "--quiet"),
        lambda: [str(cwd / ".venv" / "bin" / "python"), "-m", "entrypoints.payments.main"],
        "grpc",
        "Payments",
        {
            "identity": {
                "issuer": "https://kubernetes.default.svc.cluster.local",
                "audience": "nomnom.payments",
                "jwksFile": str(IDENTITY_FIXTURES / "jwks.json"),
                "serviceAccounts": "nomnom-orders/orders-worker=orders-worker",
                "userIssuer": "nomnom-bff",
                "userJwksFile": str(IDENTITY_FIXTURES / "bff-jwks.json"),
                "userDelegates": "bff",
            },
            "spicedb": {"endpoint": "127.0.0.1:50051", "insecure": True},
            # Webhook-порт провайдера свободный, как и остальные порты процесса.
            "provider": {"kind": "fake", "webhookPort": free_port()},
            "eventStore": {"snapshotEvery": 5},
        },
        {
            "SPICEDB__PRESHARED_KEY": "runtime-contract",
            "DATABASE__URI": "postgresql://runtime:contract@127.0.0.1:1/payments",
            "EVENT_STORE__SUBJECT_KEKS": json.dumps({"1": "runtime-contract-kek-0123456789abcdef"}),
            "EVENT_STORE__SUBJECT_REF_KEY": "runtime-contract-ref-key-0123456789abcdef",
            "PROVIDER__WEBHOOK_SECRET": "runtime-contract-webhook-secret",
        },
    )


SERVICES = {s.name: s for s in (orders(), restaurants(), delivery(), payments())}


def get(port: int, path: str, timeout: float = 1.0) -> tuple[int, bytes]:
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=timeout)
    try:
        conn.request("GET", path)
        response = conn.getresponse()
        return response.status, response.read()
    finally:
        conn.close()


def status_or_none(port: int, path: str) -> int | None:
    try:
        return get(port, path)[0]
    except (OSError, http.client.HTTPException):
        return None


def accepts_connections(port: int) -> bool:
    try:
        with socket.create_connection(("127.0.0.1", port), timeout=0.5):
            return True
    except OSError:
        return False


def business_responds(service: Service, port: int) -> bool:
    if service.business_protocol == "grpc":
        return accepts_connections(port)
    return status_or_none(port, "/") is not None


def wait_until(predicate: Callable[[], bool], timeout: float, interval: float = 0.02) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return True
        time.sleep(interval)
    return predicate()


def merged(base: dict[str, Any], patch: dict[str, Any]) -> dict[str, Any]:
    result = dict(base)
    for key, value in patch.items():
        if isinstance(value, dict) and isinstance(result.get(key), dict):
            result[key] = merged(result[key], value)
        else:
            result[key] = value
    return result


class ServiceProcess:
    def __init__(
        self,
        service: Service,
        spec: dict[str, Any] | None = None,
        env: dict[str, str] | None = None,
        config_file: bool = True,
    ) -> None:
        self.server_port = free_port()
        self.management_port = free_port()
        config = {
            "apiVersion": API_VERSION,
            "kind": service.kind,
            "metadata": {"name": service.name},
            "spec": merged(
                merged(
                    service.spec,
                    {
                        "server": {"port": self.server_port},
                        "management": {"port": self.management_port},
                        "shutdown": {
                            "readinessDelay": f"{int(READINESS_DELAY_S * 1000)}ms",
                            "drainTimeout": f"{int(DRAIN_TIMEOUT_S * 1000)}ms",
                        },
                    },
                ),
                spec or {},
            ),
        }
        # JSON — подмножество YAML, поэтому тест обходится stdlib.
        self.config = tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False)
        json.dump(config, self.config)
        self.config.close()
        environment = {
            name: value
            for name, value in os.environ.items()
            if "__" not in name and not name.startswith("OTEL_")
        }
        environment.update(service.secrets)
        environment["POD_NAME"] = f"{service.name}-runtime-contract"
        if config_file:
            environment["CONFIG_FILE"] = self.config.name
        else:
            environment.pop("CONFIG_FILE", None)
        environment.update(env or {})
        self.log = tempfile.TemporaryFile()
        self.process = subprocess.Popen(
            service.command(), cwd=service.cwd, env=environment, stdout=self.log, stderr=self.log
        )

    def output(self) -> str:
        self.log.seek(0)
        return self.log.read().decode(errors="replace")

    def wait_started(self) -> None:
        started = wait_until(
            lambda: self.process.poll() is not None
            or status_or_none(self.management_port, "/health/startup") == 200,
            STARTUP_TIMEOUT_S,
        )
        if not started or self.process.poll() is not None:
            raise AssertionError(f"process did not start:\n{self.output()}")

    def signal(self, sig: signal.Signals) -> None:
        self.process.send_signal(sig)

    def wait_exit(self, timeout: float) -> int | None:
        try:
            return self.process.wait(timeout)
        except subprocess.TimeoutExpired:
            return None

    def stop(self) -> None:
        if self.process.poll() is None:
            self.process.kill()
            self.process.wait()
        self.log.close()
        os.unlink(self.config.name)


class RuntimeContract(unittest.TestCase):
    service: Service

    def start(
        self,
        spec: dict[str, Any] | None = None,
        env: dict[str, str] | None = None,
        config_file: bool = True,
    ) -> ServiceProcess:
        proc = ServiceProcess(self.service, spec, env, config_file)
        self.addCleanup(proc.stop)
        return proc

    def assertExitCode(self, proc: ServiceProcess, expected_zero: bool, timeout: float) -> None:
        code = proc.wait_exit(timeout)
        self.assertIsNotNone(code, f"process did not exit in {timeout}s:\n{proc.output()}")
        if expected_zero:
            self.assertEqual(code, 0, proc.output())
        else:
            self.assertNotEqual(code, 0, proc.output())

    def test_startup_live_ready_on_management_port(self) -> None:
        proc = self.start()
        proc.wait_started()

        for path in ("/health/startup", "/health/live", "/health/ready"):
            status, body = get(proc.management_port, path)
            self.assertEqual(status, 200, path)
            self.assertEqual(json.loads(body)["status"], "ok", path)
        self.assertTrue(business_responds(self.service, proc.server_port), "business port must answer")
        self.assertNotEqual(
            status_or_none(proc.server_port, "/health/ready"),
            200,
            "health must not be served on the business port",
        )

    def test_sigterm_drains_and_exits_zero(self) -> None:
        proc = self.start()
        proc.wait_started()
        # Idle HTTP keep-alive соединение не должно задерживать shutdown. Для
        # gRPC простой raw TCP socket не является корректным idle HTTP/2 client
        # (ему нужны SETTINGS/ACK), поэтому transport drain покрывают adapter tests.
        if self.service.business_protocol == "grpc":
            idle = None
        else:
            idle = http.client.HTTPConnection("127.0.0.1", proc.server_port, timeout=1)
            idle.request("GET", "/")
            idle.getresponse().read()
        if idle is not None:
            self.addCleanup(idle.close)

        signaled_at = time.monotonic()
        proc.signal(signal.SIGTERM)

        self.assertTrue(
            wait_until(lambda: status_or_none(proc.management_port, "/health/ready") == 503, 0.5),
            "readiness must fail right after SIGTERM",
        )
        status, body = get(proc.management_port, "/health/ready")
        self.assertEqual(json.loads(body).get("reason"), "shutting_down")
        self.assertEqual(get(proc.management_port, "/health/live")[0], 200)
        # Во время readiness-propagation delay экземпляр ещё обслуживает запросы.
        self.assertLess(time.monotonic() - signaled_at, READINESS_DELAY_S)
        self.assertTrue(business_responds(self.service, proc.server_port))

        self.assertTrue(
            wait_until(
                lambda: not accepts_connections(proc.server_port),
                READINESS_DELAY_S + EXIT_MARGIN_S,
            ),
            "business port must stop accepting connections after the readiness delay",
        )
        self.assertGreaterEqual(time.monotonic() - signaled_at, READINESS_DELAY_S * 0.9)
        self.assertExitCode(proc, True, DRAIN_TIMEOUT_S + EXIT_MARGIN_S)
        self.assertLess(
            time.monotonic() - signaled_at,
            READINESS_DELAY_S + DRAIN_TIMEOUT_S,
            "idle connections must not hold shutdown until the drain deadline",
        )

    def test_sigint_exits_zero(self) -> None:
        proc = self.start(spec={"shutdown": {"readinessDelay": "0ms"}})
        proc.wait_started()

        proc.signal(signal.SIGINT)
        self.assertExitCode(proc, True, DRAIN_TIMEOUT_S + EXIT_MARGIN_S)

    def test_invalid_config_fails_fast(self) -> None:
        proc = self.start(spec={"shutdown": {"drainTimeout": "0ms"}})

        self.assertExitCode(proc, False, STARTUP_TIMEOUT_S)
        self.assertIsNone(status_or_none(proc.management_port, "/health/startup"))

    def test_unknown_key_fails_fast(self) -> None:
        proc = self.start(spec={"shutdown": {"drainTimout": "20s"}})

        self.assertExitCode(proc, False, STARTUP_TIMEOUT_S)
        self.assertIn("drainTimout", proc.output())
        self.assertIsNone(status_or_none(proc.management_port, "/health/startup"))

    def test_missing_config_file_fails_fast(self) -> None:
        proc = self.start(config_file=False)

        self.assertExitCode(proc, False, STARTUP_TIMEOUT_S)
        self.assertIsNone(status_or_none(proc.management_port, "/health/startup"))

    def test_non_secret_env_is_ignored(self) -> None:
        # Несекретная настройка задаётся только файлом: env её не перекрывает.
        proc = self.start(env={"SHUTDOWN__DRAIN_TIMEOUT": "0ms"})
        proc.wait_started()

        self.assertEqual(get(proc.management_port, "/health/ready")[0], 200)

    def test_missing_postgres_dsn_fails_fast(self) -> None:
        if self.service.name != "orders":
            self.skipTest("only orders keeps its state in PostgreSQL")
        proc = self.start(env={"POSTGRES__DSN": ""})

        self.assertExitCode(proc, False, STARTUP_TIMEOUT_S)

    def test_missing_spicedb_endpoint_fails_fast(self) -> None:
        # ADR 18: процесс, проверяющий права, не стартует без SpiceDB.
        proc = self.start(spec={"spicedb": {"endpoint": ""}})

        self.assertExitCode(proc, False, STARTUP_TIMEOUT_S)
        self.assertIsNone(status_or_none(proc.management_port, "/health/startup"))

    def test_startup_failure_exits_non_zero(self) -> None:
        # Dual-stack сокет на `::` занимает порт и для IPv4, и для IPv6: иначе на macOS
        # приложение успешно слушает `[::]` рядом с занятым `0.0.0.0`.
        blocker = socket.socket(socket.AF_INET6)
        self.addCleanup(blocker.close)
        blocker.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 0)
        blocker.bind(("::", 0))
        blocker.listen()
        busy_port = blocker.getsockname()[1]

        proc = self.start(spec={"server": {"port": busy_port}})
        self.assertExitCode(proc, False, STARTUP_TIMEOUT_S)


def suite_for(names: list[str]) -> unittest.TestSuite:
    suite = unittest.TestSuite()
    loader = unittest.TestLoader()
    for name in names:
        service = SERVICES[name]
        print(f"building {name}", file=sys.stderr)
        service.build()
        case = type(f"RuntimeContract_{name}", (RuntimeContract,), {"service": service})
        suite.addTests(loader.loadTestsFromTestCase(case))
    return suite


def main() -> int:
    names = sys.argv[1:] or list(SERVICES)
    unknown = [n for n in names if n not in SERVICES]
    if unknown:
        print(f"unknown services: {unknown}; known: {list(SERVICES)}", file=sys.stderr)
        return 2
    try:
        result = unittest.TextTestRunner(verbosity=2).run(suite_for(names))
    finally:
        shutil.rmtree(BUILD_DIR, ignore_errors=True)
    return 0 if result.wasSuccessful() else 1


if __name__ == "__main__":
    sys.exit(main())
