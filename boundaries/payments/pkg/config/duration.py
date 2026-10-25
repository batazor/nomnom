"""Общий формат длительностей конфигурации."""

import re
from datetime import timedelta

# Формат длительности общий для всех языков проекта: целое число и единица ms, s или m.
_DURATION_PATTERN = re.compile(r"^(\d+)(ms|s|m)$")
_DURATION_UNITS = {
    "ms": timedelta(milliseconds=1),
    "s": timedelta(seconds=1),
    "m": timedelta(minutes=1),
}


def parse_duration(value: str) -> timedelta:
    match = _DURATION_PATTERN.match(value)
    if match is None:
        raise ValueError(f"invalid duration {value!r}: expected <int>(ms|s|m), e.g. 500ms, 5s, 1m")
    amount, unit = match.groups()
    return int(amount) * _DURATION_UNITS[unit]


def _duration(value: object) -> timedelta:
    # Числа и ISO 8601, которые pydantic принимает для timedelta, не допускаются:
    # единственный внешний формат — строка <int>(ms|s|m).
    if isinstance(value, timedelta):
        return value
    if isinstance(value, str):
        return parse_duration(value.strip())
    raise ValueError(f"invalid duration {value!r}: expected <int>(ms|s|m)")
