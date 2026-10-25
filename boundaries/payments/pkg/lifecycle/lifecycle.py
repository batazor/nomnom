import logging
import signal
import threading
import time
from dataclasses import dataclass
from types import FrameType
from typing import Any, Protocol

from pkg.lifecycle.errors import LifecycleError
from pkg.lifecycle.health import Health, ManagementServer

logger = logging.getLogger(__name__)

# Период проверки сигналов и внешнего stop event. Обработчик сигнала только
# выставляет флаг: блокировки из обработчика сигнала могут привести к deadlock.
_POLL_INTERVAL = 0.05

# Запас на возврат run() после shutdown(): worker уже прекратил работу к deadline.
_RUN_JOIN_GRACE = 1.0

_SIGNALS = (signal.SIGTERM, signal.SIGINT)


@dataclass(frozen=True)
class LifecycleConfig:
    # Порт технического порта с /health/*; 0 — свободный порт (для тестов).
    management_port: int
    # Ожидание распространения not ready до остановки приёма работы, в секундах.
    readiness_delay: float
    # Deadline на завершение начатой работы, в секундах.
    drain_timeout: float
    # Технический порт слушает адрес pod, а не localhost: probes приходят с адреса узла.
    management_host: str = "0.0.0.0"


class Worker(Protocol):
    """Единица работы процесса, которой владеет composition root:
    HTTP/gRPC server, consumer или background worker."""

    def start(self) -> None:
        """Захватывает ресурсы, например listener. Исключение прерывает startup."""
        ...

    def run(self) -> None:
        """Обслуживает работу до shutdown и возвращается после штатной остановки."""
        ...

    def shutdown(self, deadline: float) -> None:
        """Прекращает приём новой работы и дожидается начатой до deadline
        (значение time.monotonic). Исключение означает, что drain не уложился."""
        ...


class _RunThread(threading.Thread):
    def __init__(self, worker: Worker, exited: threading.Event) -> None:
        # Daemon: зависший worker не блокирует выход процесса после ошибки lifecycle.
        super().__init__(name="worker", daemon=True)
        self._worker = worker
        self._exited = exited
        self.error: BaseException | None = None

    def run(self) -> None:
        try:
            self._worker.run()
        except BaseException as error:
            self.error = error
        finally:
            self._exited.set()


class Lifecycle:
    """Runtime contract процесса (docs/cloud-native-runtime.md): health state,
    технический порт, обработка SIGTERM, readiness-propagation delay и drain.
    Бизнес-код от него не зависит."""

    def __init__(self, config: LifecycleConfig) -> None:
        self._config = config
        self.health = Health()
        self._management: ManagementServer | None = None
        self._signal: int | None = None

    @property
    def management_port(self) -> int | None:
        """Фактически занятый технический порт; None до его открытия."""
        management = self._management
        return None if management is None else management.port

    def run(self, worker: Worker, stop_event: threading.Event | None = None) -> None:
        """Выполняет lifecycle: технический порт → start → ready → SIGTERM/SIGINT
        или stop_event → not ready → readiness delay → shutdown → выход.
        Нормальный возврат означает штатный shutdown; любая ошибка — исключение."""
        previous_handlers = self._install_signal_handlers()
        try:
            self._management = self._serve_management()
            try:
                self._run_worker(worker, stop_event)
            finally:
                # Технический порт закрывается последним: до этого момента probes
                # видят live=200 и ready=503.
                self._management.close()
        finally:
            _restore_signal_handlers(previous_handlers)

    def _run_worker(self, worker: Worker, stop_event: threading.Event | None) -> None:
        try:
            worker.start()
        except Exception as error:
            raise LifecycleError(f"start: {error}") from error

        exited = threading.Event()
        run_thread = _RunThread(worker, exited)
        run_thread.start()
        self.health.mark_started()

        while not self._stop_requested(stop_event):
            if exited.wait(_POLL_INTERVAL):
                self.health.mark_shutting_down()
                raise _stopped_unexpectedly(run_thread.error)

        # Повторный сигнал больше не перехватывается и завершает процесс сразу.
        _reset_signal_handlers()
        self.health.mark_shutting_down()
        logger.info(
            "shutting down: readiness delay %.3fs, drain timeout %.3fs",
            self._config.readiness_delay,
            self._config.drain_timeout,
        )

        if exited.wait(self._config.readiness_delay):
            raise _stopped_unexpectedly(run_thread.error)

        deadline = time.monotonic() + self._config.drain_timeout
        errors: list[BaseException] = []
        try:
            worker.shutdown(deadline)
        except Exception as error:
            errors.append(error)

        run_thread.join(max(0.0, deadline - time.monotonic()) + _RUN_JOIN_GRACE)
        if run_thread.is_alive():
            errors.append(LifecycleError("worker run did not return after shutdown"))
        elif run_thread.error is not None:
            errors.append(run_thread.error)

        if errors:
            message = "; ".join(str(error) or type(error).__name__ for error in errors)
            raise LifecycleError(f"shutdown: {message}") from errors[0]

    def _stop_requested(self, stop_event: threading.Event | None) -> bool:
        return self._signal is not None or (stop_event is not None and stop_event.is_set())

    def _serve_management(self) -> ManagementServer:
        host, port = self._config.management_host, self._config.management_port
        try:
            management = ManagementServer((host, port), self.health)
        except OSError as error:
            raise LifecycleError(f"listen management {host}:{port}: {error}") from error
        management.start()
        logger.info("management listening on :%d", management.port)
        return management

    def _install_signal_handlers(self) -> dict[int, Any] | None:
        # signal.signal доступен только из главного потока; в тестах lifecycle
        # запускается в отдельном потоке и останавливается через stop_event.
        if threading.current_thread() is not threading.main_thread():
            return None
        previous: dict[int, Any] = {signum: signal.getsignal(signum) for signum in _SIGNALS}
        for signum in _SIGNALS:
            signal.signal(signum, self._handle_signal)
        return previous

    def _handle_signal(self, signum: int, frame: FrameType | None) -> None:
        self._signal = signum


def _reset_signal_handlers() -> None:
    if threading.current_thread() is not threading.main_thread():
        return
    for signum in _SIGNALS:
        signal.signal(signum, signal.SIG_DFL)


def _restore_signal_handlers(previous: dict[int, Any] | None) -> None:
    if previous is None:
        return
    for signum, handler in previous.items():
        signal.signal(signum, handler)


def _stopped_unexpectedly(error: BaseException | None) -> LifecycleError:
    reason = "no error reported" if error is None else str(error) or type(error).__name__
    stopped = LifecycleError(f"worker stopped before shutdown: {reason}")
    stopped.__cause__ = error
    return stopped
