"""Telemetry recorded only after the outer Unit of Work commits.

A ``CommitScope`` collects values noted inside a Unit of Work — Domain Events of the
business counters, applied FSM transitions — and records them after the outer Unit of
Work commits. A rollback discards them; a retried attempt (ADR 20) starts from an empty
collection, so the values of a rolled back attempt are never recorded. Outside a Unit of
Work the values are already durable and are recorded at once.

``ObservedUnitOfWork`` wraps the ``UnitOfWork`` port in the composition root and opens
the scopes of one Unit of Work.
"""

from collections.abc import Callable, Iterator, Sequence
from contextlib import AbstractContextManager, ExitStack, contextmanager
from contextvars import ContextVar
from typing import Protocol, TypeVar

from pkg.application import UnitOfWork

T = TypeVar("T")


class CommitScope[V]:
    """Values of one Unit of Work handed to ``record`` after the outer commit."""

    def __init__(self, name: str, record: Callable[[Sequence[V]], None]) -> None:
        self._collected: ContextVar[list[V] | None] = ContextVar(name, default=None)
        self._record = record

    @contextmanager
    def track(self) -> Iterator[Callable[[], None]]:
        """Collect the values of one Unit of Work and record them if the block succeeds.

        Yields the restart of an attempt: it drops the values noted so far. A nested
        block joins the outer collection, its restart does nothing. An exception
        discards the values.
        """
        if self._collected.get() is not None:
            yield _keep
            return
        values: list[V] = []
        token = self._collected.set(values)
        try:
            yield values.clear
        finally:
            self._collected.reset(token)
        self._record(values)

    def add(self, values: Sequence[V]) -> None:
        """Note values for the active Unit of Work; outside one they are recorded at once."""
        collected = self._collected.get()
        if collected is None:
            self._record(values)
        else:
            collected.extend(values)


def _keep() -> None:
    return None


class Tracked(Protocol):
    """Telemetry with a scope per Unit of Work."""

    def track(self) -> AbstractContextManager[Callable[[], None]]:
        """Scope of one Unit of Work, as ``CommitScope.track``."""
        ...


class ObservedUnitOfWork:
    """``UnitOfWork`` that records the scoped telemetry after the outer transaction commits."""

    def __init__(self, inner: UnitOfWork, *tracked: Tracked) -> None:
        self._inner = inner
        self._tracked = tracked

    def run(self, work: Callable[[], T]) -> T:
        """Run ``work`` in the inner Unit of Work; every attempt starts with empty scopes."""
        with ExitStack() as stack:
            restarts = [stack.enter_context(t.track()) for t in self._tracked]

            def attempt() -> T:
                for restart in restarts:
                    restart()
                return work()

            return self._inner.run(attempt)
