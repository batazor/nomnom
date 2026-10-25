"""Structured JSON logs on stdout (docs/observability.md#логи).

One JSON object per record and line: ``timestamp`` (UTC, RFC 3339 with milliseconds),
``severity``, ``message``, the resource identity (``service.*``,
``deployment.environment.name``), ``logger``, ``trace_id``/``span_id`` of the active
span (absent outside a span), allowlisted operation fields passed with ``extra`` and an
``exception`` object. Alloy reads these field names (ops/kind Alloy ``app_logs``).

Text is redacted before it is written: bearer tokens, JWTs, credentials in URLs,
``key=value`` secrets and card-like numbers are replaced with ``[REDACTED]``.
"""

import json
import logging
import re
import sys
import traceback
from collections.abc import Mapping
from datetime import UTC, datetime
from typing import Any, TextIO, override

from opentelemetry import trace

# Нормализованные уровни: Loki label severity, OTLP severity_text.
_SEVERITY = {
    logging.DEBUG: "DEBUG",
    logging.INFO: "INFO",
    logging.WARNING: "WARN",
    logging.ERROR: "ERROR",
    logging.CRITICAL: "FATAL",
}

# Поля операции, которые код может передать через ``extra``; всё остальное отбрасывается,
# чтобы в лог не попали payload, subject или секреты.
OPERATION_FIELDS = frozenset(
    {
        "operation",
        "outcome",
        "error.type",
        "nomnom.usecase.name",
        "nomnom.event.type",
        "messaging.destination.name",
        "payment_id",
        "operation_id",
        "order_id",
        "event_id",
    }
)

REDACTED = "[REDACTED]"

_REDACTIONS: tuple[tuple[re.Pattern[str], str], ...] = (
    # Authorization: Bearer <token>
    (re.compile(r"(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]+"), r"\1 " + REDACTED),
    # JWT / JWS: three base64url segments.
    (re.compile(r"\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*"), REDACTED),
    # user:password@ в DSN и URL.
    (re.compile(r"(?i)\b([a-z][a-z0-9+.-]*://[^/\s:@]+):[^@\s/]+@"), r"\1:" + REDACTED + "@"),
    # password=..., secret: ..., token=..., api_key=..., preshared_key=...
    (
        re.compile(
            r"(?i)\b([\w.-]*(?:password|passwd|secret|token|api[_-]?key|preshared[_-]?key|"
            r"signature|cookie|credential)[\w.-]*)(\s*[=:]\s*)(\"[^\"]*\"|'[^']*'|[^\s,;&]+)"
        ),
        r"\1\2" + REDACTED,
    ),
)

# Кандидаты номеров карт: 13–19 цифр, допускаются пробелы и дефисы между группами.
_CARD_CANDIDATE = re.compile(r"\b(?:\d[ -]?){12,18}\d\b")


def _luhn_valid(digits: str) -> bool:
    total = 0
    for index, char in enumerate(reversed(digits)):
        digit = int(char)
        if index % 2:
            digit = digit * 2 - 9 if digit > 4 else digit * 2
        total += digit
    return total % 10 == 0


def _redact_card(match: re.Match[str]) -> str:
    digits = re.sub(r"[ -]", "", match.group(0))
    return REDACTED if _luhn_valid(digits) else match.group(0)


def redact(text: str) -> str:
    """Replace secrets and Luhn-valid payment card numbers in ``text`` with ``[REDACTED]``."""
    for pattern, replacement in _REDACTIONS:
        text = pattern.sub(replacement, text)
    return _CARD_CANDIDATE.sub(_redact_card, text)


class JsonFormatter(logging.Formatter):
    """Formats a record as one JSON line of the log schema with the process identity."""

    def __init__(self, identity: Mapping[str, str]) -> None:
        super().__init__()
        self._identity = dict(identity)

    @override
    def format(self, record: logging.LogRecord) -> str:
        entry: dict[str, Any] = {
            "timestamp": datetime.fromtimestamp(record.created, UTC)
            .isoformat(timespec="milliseconds")
            .replace("+00:00", "Z"),
            "severity": _severity(record.levelno),
            "message": redact(record.getMessage()),
            **self._identity,
            "logger": record.name,
        }
        span_context = trace.get_current_span().get_span_context()
        if span_context.is_valid:
            entry["trace_id"] = trace.format_trace_id(span_context.trace_id)
            entry["span_id"] = trace.format_span_id(span_context.span_id)
        for key in OPERATION_FIELDS:
            value = record.__dict__.get(key)
            if isinstance(value, str | int | float | bool):
                entry[key] = redact(value) if isinstance(value, str) else value
        exc_info = record.exc_info
        if isinstance(exc_info, tuple) and exc_info[1] is not None:
            entry["exception"] = _exception(exc_info[1])
        return json.dumps(entry, ensure_ascii=False, default=str)


def _severity(level: int) -> str:
    for threshold in (logging.CRITICAL, logging.ERROR, logging.WARNING, logging.INFO):
        if level >= threshold:
            return _SEVERITY[threshold]
    return _SEVERITY[logging.DEBUG]


def _exception(error: BaseException) -> dict[str, str]:
    """OTel ``exception.*`` fields; message and stack trace are redacted."""
    return {
        "type": f"{type(error).__module__}.{type(error).__qualname__}",
        "message": redact(str(error)),
        "stacktrace": redact("".join(traceback.format_exception(error))),
    }


class StdoutHandler(logging.StreamHandler[TextIO]):
    """Writes to the current ``sys.stdout``, so a redirected stdout (tests) is honoured."""

    def __init__(self) -> None:
        super().__init__(sys.stdout)

    @override
    def emit(self, record: logging.LogRecord) -> None:
        # Вызывается под lock handler: поток всегда текущий sys.stdout.
        self.stream = sys.stdout
        super().emit(record)


def configure_logging(identity: Mapping[str, str], level: int = logging.INFO) -> None:
    """Route every logger (gRPC, lifecycle, SDK) to one JSON handler on stdout.

    Replaces the root handlers, so a repeated call (tests) does not duplicate lines.
    """
    handler = StdoutHandler()
    handler.setFormatter(JsonFormatter(identity))
    root = logging.getLogger()
    for existing in list(root.handlers):
        root.removeHandler(existing)
    root.addHandler(handler)
    root.setLevel(level)
    # Логгеры библиотек пишут через root: свои handlers им не нужны.
    logging.captureWarnings(True)
