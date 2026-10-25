"""Exact monetary value used by the payments domain."""

import re
from dataclasses import dataclass
from decimal import Decimal, InvalidOperation
from typing import Union


class InvalidMoney(ValueError):
    """Raised when a monetary value violates its invariants."""


class CurrencyMismatch(ValueError):
    """Raised when arithmetic mixes different currencies."""


_CURRENCY_PATTERN = re.compile(r"^[A-Z]{3}$")


@dataclass(frozen=True)
class Money:
    amount: Decimal
    currency: str

    def __post_init__(self) -> None:
        if not isinstance(self.amount, Decimal):
            raise InvalidMoney("amount must be Decimal")
        if not self.amount.is_finite():
            raise InvalidMoney("amount must be finite")
        if self.amount < 0:
            raise InvalidMoney("amount must not be negative")
        if not _CURRENCY_PATTERN.fullmatch(self.currency):
            raise InvalidMoney("currency must be an ISO 4217 code")

    @classmethod
    def parse(cls, amount: Union[str, Decimal], currency: str) -> "Money":
        if isinstance(amount, float):
            raise InvalidMoney("binary floating-point amounts are not supported")
        try:
            exact_amount = amount if isinstance(amount, Decimal) else Decimal(amount)
        except (InvalidOperation, ValueError) as error:
            raise InvalidMoney("amount must be an exact decimal") from error
        return cls(exact_amount, currency)

    def add(self, other: "Money") -> "Money":
        if self.currency != other.currency:
            raise CurrencyMismatch("money arithmetic requires one currency")
        return Money(self.amount + other.amount, self.currency)
