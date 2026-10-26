"""Semconv db.client.connection.* metrics of the payments psycopg pool."""

import unittest
from datetime import timedelta
from types import TracebackType
from typing import Any, cast, override
from unittest.mock import patch

import psycopg
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import (
    HistogramDataPoint,
    InMemoryMetricReader,
    NumberDataPoint,
)
from psycopg_pool import ConnectionPool, PoolTimeout

from infrastructure.postgres import create_database
from infrastructure.postgres.database import connect_timeout_seconds
from infrastructure.postgres.pool_metrics import PoolMetrics
from pkg.config import DatabaseSettings


class FakeConnection:
    def __init__(self) -> None:
        self.exits: list[type[BaseException] | None] = []

    def __enter__(self) -> FakeConnection:
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        tb: TracebackType | None,
    ) -> None:
        self.exits.append(exc_type)


class FakePool:
    """``getconn``/``putconn``/``get_stats`` of ``ConnectionPool`` without a server."""

    def __init__(self, clock: FakeClock, wait: float = 0.0) -> None:
        self.clock = clock
        self.wait = wait
        self.timeout = False
        self.closed = False
        self.conn = FakeConnection()
        self.returned: list[FakeConnection] = []
        self.stats = {
            "pool_min": 1,
            "pool_max": 3,
            "pool_size": 3,
            "pool_available": 1,
            "requests_waiting": 2,
        }

    def getconn(self, timeout: float | None = None) -> FakeConnection:
        self.clock.now += self.wait
        if self.timeout:
            raise PoolTimeout("couldn't get a connection after 5.00 sec")
        return self.conn

    def putconn(self, conn: FakeConnection) -> None:
        self.returned.append(conn)

    def get_stats(self) -> dict[str, int]:
        return dict(self.stats)


class FakeClock:
    def __init__(self) -> None:
        self.now = 100.0

    def __call__(self) -> float:
        return self.now


class PoolMetricsTest(unittest.TestCase):
    @override
    def setUp(self) -> None:
        self.reader = InMemoryMetricReader()
        provider = MeterProvider(metric_readers=[self.reader])
        self.clock = FakeClock()
        self.pool = FakePool(self.clock, wait=0.02)
        self.metrics = PoolMetrics("payments", provider.get_meter("test"), self.clock)
        self.metrics.bind(cast(ConnectionPool, self.pool))

    def points(self) -> dict[str, list[Any]]:
        result: dict[str, list[Any]] = {}
        data = self.reader.get_metrics_data()
        if data is None:
            return result
        for resource in data.resource_metrics:
            for scope in resource.scope_metrics:
                for metric in scope.metrics:
                    result[metric.name] = list(metric.data.data_points)
        return result

    def test_observes_pool_stats(self) -> None:
        points = self.points()

        count = {
            dict(cast(NumberDataPoint, p).attributes or {})["db.client.connection.state"]: p.value
            for p in points["db.client.connection.count"]
        }
        self.assertEqual(count, {"used": 2, "idle": 1})
        for name, expected in (
            ("db.client.connection.max", 3),
            ("db.client.connection.idle.min", 1),
            ("db.client.connection.pending_requests", 2),
        ):
            with self.subTest(metric=name):
                (point,) = points[name]
                self.assertEqual(point.value, expected)
                self.assertEqual(point.attributes["db.client.connection.pool.name"], "payments")

    def test_closed_pool_reports_no_gauges(self) -> None:
        self.pool.closed = True

        points = self.points()

        for name in ("db.client.connection.count", "db.client.connection.max"):
            self.assertFalse(points.get(name), name)

    def test_records_create_time_of_established_connections(self) -> None:
        connection_class = self.metrics.connection_class()
        established = object()

        def connect(cls: type, conninfo: str = "", **kwargs: object) -> object:
            self.clock.now += 0.04
            return established

        with patch.object(psycopg.Connection, "connect", classmethod(connect)):
            conn = connection_class.connect("postgresql://payments@db/payments", connect_timeout=5)

        self.assertIs(conn, established)
        self.assertTrue(issubclass(connection_class, psycopg.Connection))
        (create,) = cast(
            list[HistogramDataPoint], self.points()["db.client.connection.create_time"]
        )
        self.assertEqual(create.count, 1)
        self.assertAlmostEqual(create.sum, 0.04)

    def test_failed_connect_records_no_create_time(self) -> None:
        connection_class = self.metrics.connection_class()

        def connect(cls: type, conninfo: str = "", **kwargs: object) -> object:
            raise psycopg.OperationalError("connection refused")

        with (
            patch.object(psycopg.Connection, "connect", classmethod(connect)),
            self.assertRaises(psycopg.OperationalError),
        ):
            connection_class.connect("postgresql://payments@db/payments")

        self.assertNotIn("db.client.connection.create_time", self.points())

    def test_borrow_requires_bound_pool(self) -> None:
        with self.assertRaises(RuntimeError), PoolMetrics("payments").borrow():
            pass

    def test_measures_wait_and_use_time(self) -> None:
        with self.metrics.borrow() as conn:
            self.clock.now += 0.3

        self.assertIs(conn, cast(psycopg.Connection, self.pool.conn))
        self.assertEqual(self.pool.returned, [self.pool.conn])
        self.assertEqual(self.pool.conn.exits, [None])
        points = self.points()
        (wait,) = cast(list[HistogramDataPoint], points["db.client.connection.wait_time"])
        self.assertEqual(wait.count, 1)
        self.assertAlmostEqual(wait.sum, 0.02)
        self.assertEqual(wait.attributes, {"db.client.connection.pool.name": "payments"})
        self.assertEqual(wait.explicit_bounds[0], 0.0005)
        (use,) = cast(list[HistogramDataPoint], points["db.client.connection.use_time"])
        self.assertAlmostEqual(use.sum, 0.3)
        self.assertNotIn("db.client.connection.timeouts", points)

    def test_returns_connection_when_work_fails(self) -> None:
        with self.assertRaises(RuntimeError), self.metrics.borrow():
            raise RuntimeError("boom")

        self.assertEqual(self.pool.returned, [self.pool.conn])
        self.assertEqual(self.pool.conn.exits, [RuntimeError])
        self.assertIn("db.client.connection.use_time", self.points())

    def test_counts_acquire_timeouts(self) -> None:
        self.pool.timeout = True

        with self.assertRaises(PoolTimeout), self.metrics.borrow():
            self.fail("no connection is yielded on timeout")

        points = self.points()
        (timeouts,) = points["db.client.connection.timeouts"]
        self.assertEqual(timeouts.value, 1)
        self.assertNotIn("db.client.connection.wait_time", points)
        self.assertEqual(self.pool.returned, [])


class CreateDatabaseTest(unittest.TestCase):
    def test_pool_follows_settings(self) -> None:
        settings = DatabaseSettings.model_validate(
            {
                "DATABASE__URL": "postgresql://payments@127.0.0.1:1/x",
                "DATABASE__MAX_CONNS": "3",
                "DATABASE__MIN_CONNS": "0",
                "DATABASE__MAX_CONN_LIFETIME": "30m",
                "DATABASE__MAX_CONN_IDLE_TIME": "5m",
                "DATABASE__CONNECT_TIMEOUT": "2500ms",
                "DATABASE__ACQUIRE_TIMEOUT": "4s",
            }
        )
        database = create_database(settings)
        self.addCleanup(database.close)
        pool = database._pool

        self.assertEqual((pool.min_size, pool.max_size), (0, 3))
        self.assertEqual(pool.timeout, 4.0)
        self.assertEqual(pool.max_lifetime, 1800.0)
        self.assertEqual(pool.max_idle, 300.0)
        kwargs = pool.kwargs
        assert isinstance(kwargs, dict)
        self.assertEqual(set(kwargs), {"connect_timeout", "cursor_factory"})
        self.assertEqual(kwargs["connect_timeout"], 3)
        self.assertTrue(issubclass(kwargs["cursor_factory"], psycopg.Cursor))
        self.assertEqual(pool.name, "payments")
        self.assertTrue(issubclass(pool.connection_class, psycopg.Connection))
        self.assertIsNot(pool.connection_class, psycopg.Connection)

    def test_connect_timeout_rounds_up_to_whole_seconds(self) -> None:
        for value, expected in ((timedelta(milliseconds=200), 1), (timedelta(seconds=5), 5)):
            with self.subTest(value=value):
                self.assertEqual(connect_timeout_seconds(value), expected)


if __name__ == "__main__":
    unittest.main()
