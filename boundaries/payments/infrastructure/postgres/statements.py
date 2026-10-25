"""PostgreSQL client telemetry of every statement (database semantic conventions v1.44.0).

One ``SpanKind.CLIENT`` span per statement, named ``{db.operation.name}
{db.collection.name}``, and one ``db.client.operation.duration`` point with
``db.system.name=postgresql``, ``db.operation.name``, ``db.collection.name`` when the
statement names one table, and on failure ``error.type`` (the SQLSTATE of a database
error, otherwise a closed class); the span also carries ``db.response.status_code``.
Neither the statement text nor bind parameters are recorded: only the operation keyword
and the table come from the statement.

Spans are created only inside an existing trace (a use case, a gRPC call): a statement
without a parent would be a one-span trace, noise rather than diagnosis.
The duration is recorded for every statement.

Statements are measured by the cursor class of the pooled connections, so repositories,
queries and the Outbox are covered without code of their own; migrations run on their
own connection before the pool opens.
"""

import functools
import time
from collections.abc import Iterable, Iterator
from contextlib import contextmanager
from dataclasses import dataclass
from typing import Any, Self, override

import psycopg
from opentelemetry import metrics, trace
from opentelemetry.trace import SpanKind, Status, StatusCode
from psycopg import sql
from psycopg.abc import Params, Query

INSTRUMENTATION = "nomnom.payments.postgres"

DB_SYSTEM_NAME = "db.system.name"
DB_OPERATION_NAME = "db.operation.name"
DB_COLLECTION_NAME = "db.collection.name"
DB_RESPONSE_STATUS_CODE = "db.response.status_code"
ERROR_TYPE = "error.type"

POSTGRESQL = "postgresql"
# Ошибка вне закрытого набора классов (semconv error.type).
OTHER_ERROR = "_OTHER"

# Ключевые слова, которые пишутся как db.operation.name; DDL миграций его не получает.
OPERATIONS = frozenset(
    {"SELECT", "INSERT", "UPDATE", "DELETE", "WITH", "BEGIN", "COMMIT", "ROLLBACK", "LOCK"}
)

# Semconv advice db.client.operation.duration: как у MongoDB-операций restaurants.
OPERATION_BUCKETS = (0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0, 10.0)

_SEPARATORS = str.maketrans(dict.fromkeys("(),;", " "))


@dataclass(frozen=True)
class Statement:
    """Low-cardinality summary of one statement."""

    operation: str | None = None
    collection: str | None = None

    @property
    def span_name(self) -> str:
        """``{operation} {collection}``, the operation alone, or ``postgresql``."""
        if self.operation is None:
            return POSTGRESQL
        if self.collection is None:
            return self.operation
        return f"{self.operation} {self.collection}"

    def attributes(self) -> dict[str, str]:
        """``db.*`` attributes of the statement."""
        attributes = {DB_SYSTEM_NAME: POSTGRESQL}
        if self.operation is not None:
            attributes[DB_OPERATION_NAME] = self.operation
        if self.collection is not None:
            attributes[DB_COLLECTION_NAME] = self.collection
        return attributes


@functools.cache
def summarize(sql: str) -> Statement | None:
    """Operation keyword and table of ``sql``; ``None`` for an empty statement.

    Statements are static strings of the code, so the result is cached.
    """
    words = sql.translate(_SEPARATORS).upper().split()
    if not words:
        return None
    if words[0] not in OPERATIONS:
        return Statement()
    operation = words[0]

    def after(keyword: str) -> str | None:
        if keyword not in words:
            return None
        index = words.index(keyword) + 1
        return words[index] if index < len(words) else None

    match operation:
        case "SELECT" | "DELETE":
            table = after("FROM")
        case "INSERT":
            table = after("INTO")
        case "UPDATE" | "LOCK":
            table = words[2] if len(words) > 2 and words[1] == "TABLE" else after(operation)
        case _:
            table = None
    if table is None or not all(c.isalnum() or c in "_." for c in table):
        return Statement(operation)
    return Statement(operation, table.lower())


def _statement(query: Query) -> Statement | None:
    match query:
        case str():
            return summarize(query)
        case bytes():
            return summarize(query.decode(errors="replace"))
        case sql.Composable():
            return summarize(query.as_string())
        case _:
            # t-string: ключевое слово и таблица — в литеральной части, не в значениях.
            return summarize(query.strings[0])


def error_type(error: Exception) -> str:
    """``error.type`` of a failed statement: the SQLSTATE, otherwise a closed class."""
    if isinstance(error, psycopg.Error):
        if error.sqlstate:
            return error.sqlstate
        if isinstance(error, psycopg.OperationalError):
            return "operational"
        if isinstance(error, psycopg.InterfaceError):
            return "interface"
        return "database"
    return OTHER_ERROR


class StatementTelemetry:
    """Client spans and ``db.client.operation.duration`` of the payments statements.

    Prometheus name: ``db_client_operation_duration_seconds``. Providers default to the
    global ones: no-op until telemetry setup.
    """

    def __init__(
        self,
        tracer_provider: trace.TracerProvider | None = None,
        meter_provider: metrics.MeterProvider | None = None,
    ) -> None:
        self._tracer = trace.get_tracer(INSTRUMENTATION, tracer_provider=tracer_provider)
        self._duration = metrics.get_meter(
            INSTRUMENTATION, meter_provider=meter_provider
        ).create_histogram(
            "db.client.operation.duration",
            unit="s",
            description="Duration of database client operations.",
            explicit_bucket_boundaries_advisory=list(OPERATION_BUCKETS),
        )

    def cursor_class(self) -> type[psycopg.Cursor[Any]]:
        """Cursor class for the pooled connections (``cursor_factory``): measures every statement.

        ``Connection.execute`` runs through the cursor too.
        """
        telemetry = self

        class ObservedCursor(psycopg.Cursor[Any]):
            @override
            def execute(
                self,
                query: Query,
                params: Params | None = None,
                *,
                prepare: bool | None = None,
                binary: bool | None = None,
            ) -> Self:
                with telemetry.observe(query):
                    return super().execute(query, params, prepare=prepare, binary=binary)

            @override
            def executemany(
                self, query: Query, params_seq: Iterable[Params], *, returning: bool = False
            ) -> None:
                with telemetry.observe(query):
                    super().executemany(query, params_seq, returning=returning)

        return ObservedCursor

    @contextmanager
    def observe(self, query: Query) -> Iterator[None]:
        """Measure one statement; its result and exceptions pass through unchanged.

        The empty statement of the pool connection check is not an operation and is not
        measured.
        """
        statement = _statement(query)
        if statement is None:
            yield
            return
        attributes = statement.attributes()
        span = trace.INVALID_SPAN
        if trace.get_current_span().get_span_context().is_valid:
            span = self._tracer.start_span(
                statement.span_name, kind=SpanKind.CLIENT, attributes=attributes
            )
        started = time.perf_counter()
        try:
            yield
        except Exception as error:
            kind = error_type(error)
            attributes = {**attributes, ERROR_TYPE: kind}
            if isinstance(error, psycopg.Error) and error.sqlstate:
                span.set_attribute(DB_RESPONSE_STATUS_CODE, error.sqlstate)
            # Текст ошибки может повторять значения параметров: только класс.
            span.set_attribute(ERROR_TYPE, kind)
            span.set_status(Status(StatusCode.ERROR, kind))
            raise
        finally:
            self._duration.record(time.perf_counter() - started, attributes)
            span.end()
