"""Composite Worker that runs several business ports as one."""

import threading
import time

from pkg.lifecycle.errors import LifecycleError
from pkg.lifecycle.lifecycle import Worker


class WorkerGroup:
    """Several business ports supervised as one Worker.

    ``run()`` returns as soon as any of them exits; ``shutdown()`` stops all of them
    within the same deadline.

    Raises:
        LifecycleError: The group is created without workers.
    """

    def __init__(self, *workers: Worker) -> None:
        if not workers:
            raise LifecycleError("worker group is empty")
        self._workers = workers

    def start(self) -> None:
        """Start the workers in order; on failure shut down the already started ones.

        The original start error is re-raised after the rollback.
        """
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
        """Run every worker in its own thread until the first one exits.

        Re-raises the first error reported by a worker; returns normally when a worker
        exits without an error.
        """
        exited = threading.Event()
        errors: list[BaseException] = []

        def serve(worker: Worker) -> None:
            try:
                worker.run()
            except BaseException as error:  # noqa: BLE001 - supervisor must catch every failure
                errors.append(error)
            finally:
                exited.set()

        for worker in self._workers:
            threading.Thread(target=serve, args=(worker,), name="worker", daemon=True).start()
        exited.wait()
        if errors:
            raise errors[0]

    def shutdown(self, deadline: float) -> None:
        """Shut down every worker, even if some fail, and re-raise the first failure."""
        errors: list[Exception] = []
        for worker in self._workers:
            try:
                worker.shutdown(deadline)
            except Exception as error:  # noqa: BLE001 - supervisor must catch every failure
                errors.append(error)
        if errors:
            raise errors[0]
