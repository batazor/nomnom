"""System clock adapter of the payment slice."""

from datetime import UTC, datetime


class SystemClock:
    """Clock backed by the system wall clock, in UTC."""

    def now(self) -> datetime:
        """Return the current UTC instant."""
        return datetime.now(UTC)
