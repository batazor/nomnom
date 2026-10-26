"""OTel semconv ``db.client.connection.*`` metrics of the psycopg pool.

Gauges come from ``ConnectionPool.get_stats()`` at collection time; wait and use time
and acquire timeouts are measured around every borrowed connection, create time by the
connection class the pool opens connections with. The process is
told apart by ``service.name`` (``job`` in Prometheus), the pool by
``db.client.connection.pool.name``.
"""

import time
from collections.abc import Callable, Iterable, Iterator
from contextlib import contextmanager
from typing import Any, Self

import psycopg
from opentelemetry import metrics
from opentelemetry.metrics import CallbackOptions, Observation
from psycopg_pool import ConnectionPool, PoolTimeout

INSTRUMENTATION = "nomnom.payments.postgres"

POOL_NAME_ATTRIBUTE = "db.client.connection.pool.name"
STATE_ATTRIBUTE = "db.client.connection.state"

# Выдача соединения из прогретого пула — микросекунды, новое соединение через пулер —
# миллисекунды, насыщенный пул — до acquire timeout; use time покрывает Unit of Work.
POOL_TIME_BUCKETS = (
    0.0005,
    0.001,
    0.0025,
    0.005,
    0.01,
    0.025,
    0.05,
    0.1,
    0.25,
    0.5,
    1.0,
    2.5,
    5.0,
    10.0,
    30.0,
)


class PoolMetrics:
    """Instruments of one psycopg pool (docs/observability/metric-inventory.md#db).

    Prometheus names: ``db_client_connection_count`` (``state``: idle | used),
    ``db_client_connection_max``, ``db_client_connection_idle_min``,
    ``db_client_connection_pending_requests``, ``db_client_connection_timeouts_total``,
    ``db_client_connection_wait_time_seconds``, ``db_client_connection_use_time_seconds``,
    ``db_client_connection_create_time_seconds``.

    The pool is created with ``connection_class`` and then attached with ``bind``.
    """

    def __init__(
        self,
        pool_name: str,
        meter: metrics.Meter | None = None,
        clock: Callable[[], float] = time.monotonic,
    ) -> None:
        meter = meter or metrics.get_meter(INSTRUMENTATION)
        self._pool: ConnectionPool | None = None
        self._clock = clock
        self._attributes = {POOL_NAME_ATTRIBUTE: pool_name}
        meter.create_observable_up_down_counter(
            "db.client.connection.count",
            callbacks=[self._observe_count],
            unit="{connection}",
            description="The number of connections that are currently in state described by "
            "the state attribute.",
        )
        meter.create_observable_up_down_counter(
            "db.client.connection.max",
            callbacks=[self._observe_stat("pool_max")],
            unit="{connection}",
            description="The maximum number of open connections allowed.",
        )
        meter.create_observable_up_down_counter(
            "db.client.connection.idle.min",
            callbacks=[self._observe_stat("pool_min")],
            unit="{connection}",
            description="The minimum number of idle open connections allowed.",
        )
        meter.create_observable_up_down_counter(
            "db.client.connection.pending_requests",
            callbacks=[self._observe_stat("requests_waiting")],
            unit="{request}",
            description="The number of current pending requests for an open connection.",
        )
        self._timeouts = meter.create_counter(
            "db.client.connection.timeouts",
            unit="{timeout}",
            description="The number of connection timeouts that have occurred trying to "
            "obtain a connection from the pool.",
        )
        self._wait_time = meter.create_histogram(
            "db.client.connection.wait_time",
            unit="s",
            description="The time it took to obtain an open connection from the pool.",
            explicit_bucket_boundaries_advisory=POOL_TIME_BUCKETS,
        )
        self._use_time = meter.create_histogram(
            "db.client.connection.use_time",
            unit="s",
            description="The time between borrowing a connection and returning it to the pool.",
            explicit_bucket_boundaries_advisory=POOL_TIME_BUCKETS,
        )
        self._create_time = meter.create_histogram(
            "db.client.connection.create_time",
            unit="s",
            description="The time it took to create a new connection.",
            explicit_bucket_boundaries_advisory=POOL_TIME_BUCKETS,
        )

    def bind(self, pool: ConnectionPool) -> None:
        """Attach the pool whose stats the gauges report and whose connections are borrowed."""
        self._pool = pool

    def connection_class(self) -> type[psycopg.Connection]:
        """Connection class for ``ConnectionPool``: records the create time of established connections."""
        metrics_ = self

        class MeasuredConnection(psycopg.Connection):
            @classmethod
            def connect(cls, conninfo: str = "", **kwargs: Any) -> Self:
                started = metrics_._clock()
                conn = super().connect(conninfo, **kwargs)
                metrics_._create_time.record(metrics_._clock() - started, metrics_._attributes)
                return conn

        return MeasuredConnection

    def _bound(self) -> ConnectionPool:
        if self._pool is None:
            raise RuntimeError("PoolMetrics.bind was not called")
        return self._pool

    @contextmanager
    def borrow(self, timeout: float | None = None) -> Iterator[psycopg.Connection]:
        """Borrow a pooled connection, measuring wait and use time and acquire timeouts."""
        pool = self._bound()
        started = self._clock()
        try:
            conn = pool.getconn(timeout=timeout)
        except PoolTimeout:
            self._timeouts.add(1, self._attributes)
            raise
        acquired = self._clock()
        self._wait_time.record(acquired - started, self._attributes)
        try:
            # Как ConnectionPool.connection(): commit при выходе, rollback при ошибке.
            with conn:
                yield conn
        finally:
            pool.putconn(conn)
            self._use_time.record(self._clock() - acquired, self._attributes)

    def _stats(self) -> dict[str, int] | None:
        # Закрытый пул больше не сообщает значений: серии исчезают вместе с процессом.
        if self._pool is None or self._pool.closed:
            return None
        return self._pool.get_stats()

    def _observe_count(self, _options: CallbackOptions) -> Iterable[Observation]:
        stats = self._stats()
        if stats is None:
            return []
        size = stats.get("pool_size", 0)
        idle = stats.get("pool_available", 0)
        return [
            Observation(max(0, size - idle), {**self._attributes, STATE_ATTRIBUTE: "used"}),
            Observation(idle, {**self._attributes, STATE_ATTRIBUTE: "idle"}),
        ]

    def _observe_stat(self, key: str) -> Callable[[CallbackOptions], Iterable[Observation]]:
        def observe(_options: CallbackOptions) -> Iterable[Observation]:
            stats = self._stats()
            if stats is None:
                return []
            return [Observation(stats.get(key, 0), self._attributes)]

        return observe
