"""Unit of Work port: the local transaction boundary of one application use case."""

from collections.abc import Callable
from typing import Protocol, TypeVar

T = TypeVar("T")


class UnitOfWork(Protocol):
    def run(self, work: Callable[[], T]) -> T:
        """Execute ``work`` in one local transaction of the payments storage.

        Commits when ``work`` returns and rolls back every change when it raises; the
        exception is propagated unchanged. ``work`` must touch only local storage:
        provider, authorization and other external calls stay outside of it. A nested
        call joins the active transaction instead of opening a new one.
        """
        ...
