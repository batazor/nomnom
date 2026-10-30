"""Telemetry and retry classes of the PostgreSQL Unit of Work (``uow`` inventory section).

Names, units and attributes are fixed by ``ops/semconv/model/uow.yaml``: every attempt
of the outer Unit of Work is one transaction with ``nomnom.outcome`` committed |
rolled_back | commit_failed, every new attempt is one retry with ``nomnom.reason``
serialization | deadlock | write_conflict | other (ADR 20).
"""

from enum import StrEnum

import psycopg
from opentelemetry import metrics

from pkg.telemetry import SECONDS_BUCKETS
from pkg.usecase import ConcurrencyConflictError

INSTRUMENTATION = "nomnom.payments.uow"

OUTCOME = "nomnom.outcome"
REASON = "nomnom.reason"

# SQLSTATE класса 40 «transaction rollback»: сервер откатил транзакцию целиком.
SERIALIZATION_FAILURE = "40001"
DEADLOCK_DETECTED = "40P01"
_TRANSACTION_ROLLBACK_CLASS = "40"


class TransactionOutcome(StrEnum):
    """Closed ``nomnom.outcome`` set of one transaction."""

    COMMITTED = "committed"
    # Работа завершилась ошибкой, изменения откатаны.
    ROLLED_BACK = "rolled_back"
    # Работа выполнена, но COMMIT не прошёл: результат неизвестен и не повторяется.
    COMMIT_FAILED = "commit_failed"


class RetryReason(StrEnum):
    """Closed ``nomnom.reason`` set of transaction retries."""

    SERIALIZATION = "serialization"
    DEADLOCK = "deadlock"
    # Конфликт версии aggregate, загруженной внутри транзакции.
    WRITE_CONFLICT = "write_conflict"
    # Другой откат класса 40 (transaction rollback).
    OTHER = "other"


def retry_reason(error: Exception) -> RetryReason | None:
    """Conflict class of a failed attempt; ``None`` when the error is not retried."""
    if isinstance(error, ConcurrencyConflictError):
        return RetryReason.WRITE_CONFLICT
    if not isinstance(error, psycopg.Error) or not error.sqlstate:
        return None
    if error.sqlstate == SERIALIZATION_FAILURE:
        return RetryReason.SERIALIZATION
    if error.sqlstate == DEADLOCK_DETECTED:
        return RetryReason.DEADLOCK
    if error.sqlstate.startswith(_TRANSACTION_ROLLBACK_CLASS):
        return RetryReason.OTHER
    return None


class TransactionMetrics:
    """Instruments of the payments Unit of Work.

    Prometheus names: ``nomnom_uow_transactions_total``, ``nomnom_uow_duration_seconds``
    and ``nomnom_uow_retries_total``. Instruments are created on the global MeterProvider
    by default: no-op until telemetry setup.
    """

    def __init__(self, meter: metrics.Meter | None = None) -> None:
        meter = meter or metrics.get_meter(INSTRUMENTATION)
        self._transactions = meter.create_counter(
            "nomnom.uow.transactions",
            unit="{transaction}",
            description="Unit of Work transactions by outcome.",
        )
        self._duration = meter.create_histogram(
            "nomnom.uow.duration",
            unit="s",
            description="Duration of one Unit of Work transaction.",
            explicit_bucket_boundaries_advisory=list(SECONDS_BUCKETS),
        )
        self._retries = meter.create_counter(
            "nomnom.uow.retries",
            unit="{retry}",
            description="Unit of Work retries by conflict class.",
        )

    def finished(self, outcome: TransactionOutcome, seconds: float) -> None:
        """Record one transaction attempt."""
        attributes = {OUTCOME: outcome.value}
        self._transactions.add(1, attributes)
        self._duration.record(seconds, attributes)

    def retried(self, reason: RetryReason) -> None:
        """Record one new attempt after a conflict."""
        self._retries.add(1, {REASON: reason.value})
