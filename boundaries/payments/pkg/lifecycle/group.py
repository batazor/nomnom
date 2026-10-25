import threading
import time

from pkg.lifecycle.errors import LifecycleError
from pkg.lifecycle.lifecycle import Worker


class WorkerGroup:
    """Несколько бизнес-портов как один Worker: run() возвращается, как только
    завершился любой из них, shutdown() останавливает все в пределах deadline."""

    def __init__(self, *workers: Worker) -> None:
        if not workers:
            raise LifecycleError("worker group is empty")
        self._workers = workers

    def start(self) -> None:
        started: list[Worker] = []
        try:
            for worker in self._workers:
                worker.start()
                started.append(worker)
        except Exception:
            for worker in reversed(started):
                worker.shutdown(time.monotonic())
            raise

    def run(self) -> None:
        exited = threading.Event()
        errors: list[BaseException] = []

        def serve(worker: Worker) -> None:
            try:
                worker.run()
            except BaseException as error:
                errors.append(error)
            finally:
                exited.set()

        for worker in self._workers:
            threading.Thread(target=serve, args=(worker,), name="worker", daemon=True).start()
        exited.wait()
        if errors:
            raise errors[0]

    def shutdown(self, deadline: float) -> None:
        errors: list[Exception] = []
        for worker in self._workers:
            try:
                worker.shutdown(deadline)
            except Exception as error:
                errors.append(error)
        if errors:
            raise errors[0]
