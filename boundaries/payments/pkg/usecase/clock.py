"""Clock port: application time is passed into the domain as a value."""

from datetime import UTC, datetime
from typing import Protocol


class Clock(Protocol):
    """Source of the current instant for use cases; replaced by a fixed clock in tests."""

    def now(self) -> datetime:
        """Return the current timezone-aware instant."""
        ...


class SystemClock:
    """Clock backed by the system wall clock, in UTC."""

    def now(self) -> datetime:
        """Return the current UTC instant."""
        return datetime.now(UTC)
