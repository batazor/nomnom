"""Clock port: application time is passed into the domain as a value."""

from datetime import UTC, datetime
from typing import Protocol


class Clock(Protocol):
    def now(self) -> datetime:
        """Return the current timezone-aware instant."""
        ...


class SystemClock:
    def now(self) -> datetime:
        return datetime.now(UTC)
