"""Market of the Order a Payment belongs to (ADR 41)."""

import re
from dataclasses import dataclass
from typing import override


class InvalidMarketIdError(ValueError):
    """Raised when a Market identifier is not a valid slug."""


_MARKET_ID_PATTERN = re.compile(r"^[a-z][a-z0-9-]{1,31}$")


@dataclass(frozen=True)
class MarketId:
    """Immutable slug of a Market, for example ``berlin``.

    Raises ``InvalidMarketIdError`` on construction for an empty or malformed value.
    """

    value: str

    def __post_init__(self) -> None:
        if not isinstance(self.value, str) or not _MARKET_ID_PATTERN.fullmatch(self.value):
            raise InvalidMarketIdError("market id must match ^[a-z][a-z0-9-]{1,31}$")

    @override
    def __str__(self) -> str:
        return self.value
