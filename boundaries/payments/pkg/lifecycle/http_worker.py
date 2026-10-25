import logging
import socket
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

from pkg.lifecycle._bind import FastBindMixin
from pkg.lifecycle.errors import DrainTimeoutError, LifecycleError

logger = logging.getLogger(__name__)

_POLL_INTERVAL = 0.1


class _TrackingHTTPServer(FastBindMixin, HTTPServer):
    """HTTP server, обслуживающий каждое соединение в daemon-потоке и самостоятельно
    отслеживающий их: ThreadingMixIn либо не ждёт daemon-потоки, либо ждёт их без дедлайна."""

    allow_reuse_address = True

    def __init__(
        self,
        address: tuple[str, int],
        handler: type[BaseHTTPRequestHandler],
    ) -> None:
        super().__init__(address, handler)
        self._lock = threading.Lock()
        self._connections: dict[threading.Thread, socket.socket] = {}

    def process_request(self, request, client_address) -> None:  # type: ignore[override]
        thread = threading.Thread(
            target=self._process_request_thread,
            args=(request, client_address),
            name="http-request",
            # Daemon: зависшее соединение не блокирует выход процесса.
            daemon=True,
        )
        with self._lock:
            self._connections[thread] = request
        thread.start()

    def _process_request_thread(self, request, client_address) -> None:
        try:
            self.finish_request(request, client_address)
        except Exception:
            self.handle_error(request, client_address)
        finally:
            self.shutdown_request(request)
            with self._lock:
                self._connections.pop(threading.current_thread(), None)

    def handle_error(self, request, client_address) -> None:  # type: ignore[override]
        logger.warning("http request from %s failed", client_address, exc_info=True)

    def connections(self) -> list[tuple[threading.Thread, socket.socket]]:
        with self._lock:
            return list(self._connections.items())


class HttpWorker:
    """lifecycle.Worker бизнес-порта на основе стандартного http.server."""

    def __init__(
        self,
        address: tuple[str, int],
        handler: type[BaseHTTPRequestHandler],
        name: str = "http",
    ) -> None:
        self._address = address
        self._handler = handler
        self._name = name
        self._server: _TrackingHTTPServer | None = None

    @property
    def port(self) -> int:
        """Фактически занятый порт; полезен при bind на порт 0."""
        return self._require_server().server_address[1]

    def start(self) -> None:
        """Открывает бизнес-порт; после него процесс может стать ready."""
        try:
            self._server = _TrackingHTTPServer(self._address, self._handler)
        except OSError as error:
            host, port = self._address
            raise LifecycleError(f"listen {self._name} {host}:{port}: {error}") from error
        logger.info("%s listening on :%d", self._name, self.port)

    def run(self) -> None:
        """Обслуживает входящие запросы до вызова shutdown."""
        self._require_server().serve_forever(poll_interval=_POLL_INTERVAL)

    def shutdown(self, deadline: float) -> None:
        """Прекращает приём новых запросов, закрывает idle keep-alive соединения
        и дожидается начатых до deadline (time.monotonic); оставшиеся соединения
        закрываются принудительно."""
        server = self._require_server()
        server.shutdown()
        server.server_close()

        # HTTP/1.1 keep-alive: закрытие чтения завершает ожидание следующего запроса,
        # а текущий ответ дописывается полностью.
        for _, connection in server.connections():
            _shutdown_socket(connection, socket.SHUT_RD)

        for thread, _ in server.connections():
            thread.join(max(0.0, deadline - time.monotonic()))

        remaining = server.connections()
        if not remaining:
            return
        for _, connection in remaining:
            _shutdown_socket(connection, socket.SHUT_RDWR)
        raise DrainTimeoutError(
            f"{self._name}: {len(remaining)} request(s) still in flight after drain timeout",
        )

    def _require_server(self) -> _TrackingHTTPServer:
        if self._server is None:
            raise LifecycleError(f"{self._name} worker is not started")
        return self._server


def _shutdown_socket(connection: socket.socket, how: int) -> None:
    try:
        connection.shutdown(how)
    except OSError:
        # Соединение уже закрыто клиентом или обработчиком.
        pass
