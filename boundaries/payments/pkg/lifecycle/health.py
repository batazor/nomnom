"""Health state of the process and the management port serving /health/* probes."""

import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import cast

from pkg.lifecycle._bind import FastBindMixin

REASON_INITIALIZING = "initializing"
REASON_SHUTTING_DOWN = "shutting_down"


class Health:
    """Precomputed health state of the instance.

    Probe handlers only read it and never call dependencies. Thread-safe.
    """

    def __init__(self) -> None:
        self._started = threading.Event()
        self._shutting_down = threading.Event()

    def mark_started(self) -> None:
        """Record that startup completed; the instance becomes ready unless shutting down."""
        self._started.set()

    def mark_shutting_down(self) -> None:
        """Record that shutdown began; readiness turns off for good."""
        self._shutting_down.set()

    @property
    def started(self) -> bool:
        """Whether startup has completed."""
        return self._started.is_set()

    @property
    def shutting_down(self) -> bool:
        """Whether shutdown has begun."""
        return self._shutting_down.is_set()

    @property
    def ready(self) -> bool:
        """Whether the instance may accept new work."""
        return self.started and not self.shutting_down

    def startup_reason(self) -> str | None:
        """Reason the startup probe fails, or ``None`` once started."""
        return None if self.started else REASON_INITIALIZING

    def readiness_reason(self) -> str | None:
        """Reason the readiness probe fails, or ``None`` while ready."""
        if self.shutting_down:
            return REASON_SHUTTING_DOWN
        return self.startup_reason()


class _HealthHandler(BaseHTTPRequestHandler):
    # HTTP/1.0: соединение закрывается после ответа, keep-alive probes не держат поток.
    # Timeout не даёт зависшему клиенту бесконечно занимать поток.
    timeout = 5

    def do_GET(self) -> None:
        health = cast("ManagementServer", self.server).health
        if self.path == "/health/startup":
            self._write(health.startup_reason())
        elif self.path == "/health/live":
            # Liveness не зависит от внешних систем и остаётся успешной во время shutdown.
            self._write(None)
        elif self.path == "/health/ready":
            self._write(health.readiness_reason())
        else:
            self.send_response(404)
            self.send_header("Content-Length", "0")
            self.end_headers()

    def _write(self, reason: str | None) -> None:
        if reason is None:
            status, payload = 200, {"status": "ok"}
        else:
            status, payload = 503, {"status": "unavailable", "reason": reason}
        body = json.dumps(payload, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format: str, *args: object) -> None:
        # Probes не логируются.
        return


class ManagementServer(FastBindMixin, ThreadingHTTPServer):
    """Management port serving /health/* from its own thread."""

    # Daemon-потоки probes не задерживают закрытие порта и выход процесса.
    daemon_threads = True
    block_on_close = False
    allow_reuse_address = True

    def __init__(self, address: tuple[str, int], health: Health) -> None:
        super().__init__(address, _HealthHandler)
        self.health = health
        self._thread: threading.Thread | None = None

    @property
    def port(self) -> int:
        """Actually bound port; useful when binding to port 0."""
        return self.server_address[1]

    def start(self) -> None:
        """Start serving probes in a background daemon thread."""
        self._thread = threading.Thread(
            target=self.serve_forever,
            kwargs={"poll_interval": 0.1},
            name="management",
            daemon=True,
        )
        self._thread.start()

    def close(self) -> None:
        """Stop the management port.

        Called last, so that until then probes see live=200 and ready=503.
        """
        if self._thread is not None:
            self.shutdown()
            self._thread.join()
        self.server_close()

    def handle_error(self, request: object, client_address: object) -> None:
        """Ignore failed probe connections; they are not process errors."""
        # Оборванное probe-соединение не является ошибкой процесса.
        return
