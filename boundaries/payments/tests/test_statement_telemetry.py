"""PostgreSQL client spans and ``db.client.operation.duration`` of the pooled statements."""

import unittest
from typing import override

import pytest
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import HistogramDataPoint, InMemoryMetricReader
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter
from opentelemetry.trace import SpanKind, StatusCode
from psycopg import errors

from infrastructure.repository import (
    Database,
    DatabasePaymentRepository,
    DatabaseUnitOfWork,
    create_database,
)
from infrastructure.repository.statements import Statement, StatementTelemetry, summarize
from pkg.config import DatabaseSettings
from slices.payment.domains.payment import Money, OrderId, Payment, PaymentId


class SummaryTest(unittest.TestCase):
    def test_operation_and_table_come_from_the_statement(self) -> None:
        cases = {
            "SELECT id, state FROM payments WHERE id = %s": Statement("SELECT", "payments"),
            "INSERT INTO payment_operations (id) VALUES (%s)": Statement(
                "INSERT", "payment_operations"
            ),
            "UPDATE payments SET state = %s": Statement("UPDATE", "payments"),
            "DELETE FROM payments_outbox WHERE event_id IN (SELECT event_id FROM x)": Statement(
                "DELETE", "payments_outbox"
            ),
            "LOCK TABLE payments IN SHARE MODE": Statement("LOCK", "payments"),
            "select 1": Statement("SELECT"),
            "WITH pending AS (SELECT 1) SELECT * FROM pending": Statement("WITH"),
            "TRUNCATE payments": Statement(),
        }
        for sql, expected in cases.items():
            with self.subTest(sql=sql):
                self.assertEqual(summarize(sql), expected)

    def test_empty_statement_is_not_an_operation(self) -> None:
        self.assertIsNone(summarize("  "))

    def test_span_names(self) -> None:
        self.assertEqual(Statement("SELECT", "payments").span_name, "SELECT payments")
        self.assertEqual(Statement("SELECT").span_name, "SELECT")
        self.assertEqual(Statement().span_name, "postgresql")


@pytest.mark.usefixtures("database")
class StatementTelemetryTest(unittest.TestCase):
    dsn: str
    database: Database

    @override
    def setUp(self) -> None:
        with self.database.connection() as conn:
            conn.execute("TRUNCATE payments, payment_operations, payments_outbox, inbox")
        self.spans = InMemorySpanExporter()
        self.tracer_provider = TracerProvider()
        self.tracer_provider.add_span_processor(SimpleSpanProcessor(self.spans))
        self.reader = InMemoryMetricReader()
        telemetry = StatementTelemetry(
            self.tracer_provider, MeterProvider(metric_readers=[self.reader])
        )
        self.observed = create_database(DatabaseSettings(DATABASE__URL=self.dsn), telemetry)
        self.addCleanup(self.observed.close)
        self.payments = DatabasePaymentRepository(self.observed)

    def durations(self) -> list[dict[str, str]]:
        """Attributes of the ``db.client.operation.duration`` points recorded since setUp."""
        data = self.reader.get_metrics_data()
        assert data is not None
        result: list[dict[str, str]] = []
        for rm in data.resource_metrics:
            for sm in rm.scope_metrics:
                for metric in sm.metrics:
                    self.assertEqual(metric.name, "db.client.operation.duration")
                    self.assertEqual(metric.unit, "s")
                    for point in metric.data.data_points:
                        assert isinstance(point, HistogramDataPoint)
                        if point.count:
                            attributes = dict(point.attributes or {})
                            result.append({k: str(v) for k, v in attributes.items()})
        return result

    def test_statements_inside_a_trace_get_client_spans_and_durations(self) -> None:
        payment = Payment(PaymentId("payment-1"), OrderId("order-1"), Money.parse("1", "EUR"))
        tracer = self.tracer_provider.get_tracer("test")
        with tracer.start_as_current_span("usecase create_payment"):
            DatabaseUnitOfWork(self.observed).run(lambda: self.payments.add(payment))
            self.payments.get(payment.id)

        client = [s for s in self.spans.get_finished_spans() if s.kind is SpanKind.CLIENT]
        self.assertEqual([s.name for s in client], ["INSERT payments", "SELECT payments"])
        for span in client:
            attributes = dict(span.attributes or {})
            self.assertEqual(attributes["db.system.name"], "postgresql")
            self.assertEqual(attributes["db.collection.name"], "payments")
            self.assertNotIn("db.query.text", attributes)
            self.assertNotIn("db.statement", attributes)
        self.assertCountEqual(
            self.durations(),
            [
                {
                    "db.system.name": "postgresql",
                    "db.operation.name": "INSERT",
                    "db.collection.name": "payments",
                },
                {
                    "db.system.name": "postgresql",
                    "db.operation.name": "SELECT",
                    "db.collection.name": "payments",
                },
            ],
        )

    def test_statement_outside_a_trace_has_a_duration_but_no_span(self) -> None:
        self.payments.get(PaymentId("missing"))

        self.assertEqual(self.spans.get_finished_spans(), ())
        self.assertEqual(
            self.durations(),
            [
                {
                    "db.system.name": "postgresql",
                    "db.operation.name": "SELECT",
                    "db.collection.name": "payments",
                }
            ],
        )

    def test_failed_statement_records_the_sqlstate(self) -> None:
        tracer = self.tracer_provider.get_tracer("test")
        with (
            tracer.start_as_current_span("usecase broken"),
            self.assertRaises(errors.UndefinedTable),
            self.observed.connection() as conn,
        ):
            conn.execute("SELECT id FROM missing_table WHERE id = %s", ("secret-value",))

        (span,) = [s for s in self.spans.get_finished_spans() if s.kind is SpanKind.CLIENT]
        self.assertEqual(span.name, "SELECT missing_table")
        self.assertEqual(span.status.status_code, StatusCode.ERROR)
        attributes = dict(span.attributes or {})
        self.assertEqual(attributes["error.type"], "42P01")
        self.assertEqual(attributes["db.response.status_code"], "42P01")
        self.assertNotIn("secret-value", repr(attributes))
        self.assertEqual(span.events, ())
        self.assertEqual(
            self.durations(),
            [
                {
                    "db.system.name": "postgresql",
                    "db.operation.name": "SELECT",
                    "db.collection.name": "missing_table",
                    "error.type": "42P01",
                }
            ],
        )
