"""Unit of Work transactions, duration and bounded retries against the real PostgreSQL."""

import unittest
from typing import override

import psycopg
import pytest
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import (
    HistogramDataPoint,
    InMemoryMetricReader,
    NumberDataPoint,
)
from psycopg import errors

from infrastructure.repository import PostgresDatabase, PostgresUnitOfWork
from infrastructure.repository.database import MAX_ATTEMPTS
from infrastructure.repository.transactions import TransactionMetrics
from pkg.application import ConcurrencyConflictError

TRANSACTIONS = "nomnom.uow.transactions"
DURATION = "nomnom.uow.duration"
RETRIES = "nomnom.uow.retries"


class Flaky:
    """Work that fails with ``error`` for the first ``failures`` attempts."""

    def __init__(self, database: PostgresDatabase, error: Exception, failures: int = 1) -> None:
        self._database = database
        self._error = error
        self._failures = failures
        self.attempts = 0

    def __call__(self) -> int:
        self.attempts += 1
        with self._database.connection() as conn:
            conn.execute(
                "INSERT INTO inbox (consumer, event_id) VALUES ('uow-test', %s)",
                (f"attempt-{self.attempts}",),
            )
        if self.attempts <= self._failures:
            raise self._error
        return self.attempts


@pytest.mark.usefixtures("postgres")
class UnitOfWorkMetricsTest(unittest.TestCase):
    database: PostgresDatabase

    @override
    def setUp(self) -> None:
        with self.database.connection() as conn:
            conn.execute("TRUNCATE inbox")
        self.reader = InMemoryMetricReader()
        provider = MeterProvider(metric_readers=[self.reader])
        self.sleeps: list[float] = []
        self.uow = PostgresUnitOfWork(
            self.database, TransactionMetrics(provider.get_meter("test")), self.sleeps.append
        )

    def points(self) -> dict[str, dict[str, float]]:
        """Metric → its only attribute value → count (histograms: number of points)."""
        result: dict[str, dict[str, float]] = {}
        data = self.reader.get_metrics_data()
        if data is None:
            return result
        for rm in data.resource_metrics:
            for sm in rm.scope_metrics:
                for metric in sm.metrics:
                    for point in metric.data.data_points:
                        ((key, value),) = dict(point.attributes or {}).items()
                        expected_key = (
                            "nomnom.reason" if metric.name == RETRIES else "nomnom.outcome"
                        )
                        self.assertEqual(key, expected_key)
                        if isinstance(point, HistogramDataPoint):
                            self.assertEqual(metric.unit, "s")
                            count = float(point.count)
                        else:
                            assert isinstance(point, NumberDataPoint)
                            count = float(point.value)
                        result.setdefault(metric.name, {})[str(value)] = count
        return result

    def stored(self) -> list[str]:
        with self.database.connection() as conn:
            rows = conn.execute(
                "SELECT event_id FROM inbox WHERE consumer = 'uow-test' ORDER BY event_id"
            ).fetchall()
        return [row[0] for row in rows]

    def test_commit_is_one_committed_transaction(self) -> None:
        self.assertEqual(self.uow.run(Flaky(self.database, RuntimeError(), failures=0)), 1)

        self.assertEqual(
            self.points(), {TRANSACTIONS: {"committed": 1}, DURATION: {"committed": 1}}
        )

    def test_failed_work_is_rolled_back_without_retry(self) -> None:
        work = Flaky(self.database, RuntimeError("boom"))

        with self.assertRaises(RuntimeError):
            self.uow.run(work)

        self.assertEqual(work.attempts, 1)
        self.assertEqual(self.stored(), [])
        self.assertEqual(
            self.points(), {TRANSACTIONS: {"rolled_back": 1}, DURATION: {"rolled_back": 1}}
        )

    def test_nested_unit_of_work_joins_and_records_once(self) -> None:
        self.uow.run(lambda: self.uow.run(Flaky(self.database, RuntimeError(), failures=0)))

        self.assertEqual(self.points()[TRANSACTIONS], {"committed": 1})

    def test_failed_commit_is_commit_failed_and_not_retried(self) -> None:
        attempts: list[int] = []

        def work() -> None:
            attempts.append(1)
            with self.database.connection() as conn:
                # Отложенная проверка уникальности срабатывает только на COMMIT.
                conn.execute(
                    "CREATE TEMP TABLE deferred (id int UNIQUE DEFERRABLE INITIALLY DEFERRED) "
                    "ON COMMIT DROP"
                )
                conn.execute("INSERT INTO deferred VALUES (1), (1)")

        with self.assertRaises(errors.UniqueViolation):
            self.uow.run(work)

        self.assertEqual(len(attempts), 1)
        self.assertEqual(
            self.points(), {TRANSACTIONS: {"commit_failed": 1}, DURATION: {"commit_failed": 1}}
        )

    def test_conflicts_are_retried_by_class(self) -> None:
        cases: dict[str, Exception] = {
            "serialization": errors.SerializationFailure("could not serialize access"),
            "deadlock": errors.DeadlockDetected("deadlock detected"),
            "write_conflict": ConcurrencyConflictError("Payment", "payment-1"),
            "other": errors.TransactionRollback("transaction rollback"),
        }
        for reason, error in cases.items():
            with self.subTest(reason=reason):
                self.setUp()
                work = Flaky(self.database, error)

                self.assertEqual(self.uow.run(work), 2)

                # Изменения откатанной попытки не сохраняются.
                self.assertEqual(self.stored(), ["attempt-2"])
                self.assertEqual(
                    self.points(),
                    {
                        TRANSACTIONS: {"rolled_back": 1, "committed": 1},
                        DURATION: {"rolled_back": 1, "committed": 1},
                        RETRIES: {reason: 1},
                    },
                )
                self.assertEqual(self.sleeps, [0.01])

    def test_retries_are_bounded(self) -> None:
        work = Flaky(self.database, errors.DeadlockDetected("deadlock"), failures=MAX_ATTEMPTS)

        with self.assertRaises(errors.DeadlockDetected):
            self.uow.run(work)

        self.assertEqual(work.attempts, MAX_ATTEMPTS)
        self.assertEqual(self.sleeps, [0.01, 0.02])
        self.assertEqual(
            self.points(),
            {
                TRANSACTIONS: {"rolled_back": MAX_ATTEMPTS},
                DURATION: {"rolled_back": MAX_ATTEMPTS},
                RETRIES: {"deadlock": MAX_ATTEMPTS - 1},
            },
        )

    def test_other_database_errors_are_not_retried(self) -> None:
        work = Flaky(self.database, psycopg.errors.UniqueViolation("duplicate key"))

        with self.assertRaises(errors.UniqueViolation):
            self.uow.run(work)

        self.assertEqual(work.attempts, 1)
        self.assertNotIn(RETRIES, self.points())
