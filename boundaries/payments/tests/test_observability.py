"""Traces, metrics and logs of the payments processes against in-memory SDK exporters.

Names and attributes are the contract of ``ops/grafana/inventory`` and ``ops/semconv``.
"""

import io
import json
import logging
import sys
import threading
import time
import unittest
from collections.abc import Generator
from contextlib import contextmanager, redirect_stdout
from datetime import UTC, datetime, timedelta
from decimal import Decimal
from typing import Any, override

import grpc
import pytest
from opentelemetry import baggage, propagate, trace
from opentelemetry.context import attach, detach
from opentelemetry.sdk.metrics.export import InMemoryMetricReader
from opentelemetry.sdk.trace import ReadableSpan
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter
from opentelemetry.trace import SpanKind, StatusCode
from payment_fakes import PaymentsTestKit
from test_grpc_service import StaticSubjectResolver

from infrastructure.grpc.service import (
    GrpcWorker,
    PaymentsHandlers,
    payments_pb2,
    service_pb2,
    service_pb2_grpc,
)
from infrastructure.repository import Database
from infrastructure.repository.outbox import outbox_traceparent
from pkg import telemetry
from pkg.lifecycle import HttpWorker
from pkg.telemetry.propagation import create_propagator
from pkg.telemetry.rpc import ClientTelemetryInterceptor, ServerTelemetryInterceptor
from pkg.telemetry.usecase import UseCases
from slices.payment.applications.apply_provider_callback import ApplyProviderCallbackHandler
from slices.payment.applications.authorize_payment import AuthorizePaymentHandler
from slices.payment.applications.cancel_authorization import CancelAuthorizationHandler
from slices.payment.applications.capture_payment import CapturePaymentHandler
from slices.payment.applications.create_payment import CreatePaymentHandler
from slices.payment.applications.get_payment_status import GetPaymentStatusHandler
from slices.payment.applications.ports import (
    ProviderOperationRequest,
    ProviderUnavailableError,
)
from slices.payment.applications.refund_payment import RefundPaymentHandler
from slices.payment.domains.payment import (
    Money,
    OperationId,
    OperationKind,
    PaymentId,
    PaymentState,
)
from slices.payment.infrastructure.observability import ObservedProvider, classify_payment_error
from slices.payment.infrastructure.observability import usecases as names
from slices.payment.infrastructure.provider import (
    SANDBOX_WEBHOOK_FORMAT,
    WEBHOOK_PATH,
    FakePaymentProvider,
    WebhookVerifier,
    create_webhook_handler,
)

CALLER_TRACE_ID = "4bf92f3577b34da6a3ce929d0e0e4736"
CALLER_TRACEPARENT = f"00-{CALLER_TRACE_ID}-00f067aa0ba902b7-01"


class Signals:
    """In-memory tracer and meter providers of one test."""

    def __init__(self) -> None:
        self.spans = InMemorySpanExporter()
        resource = telemetry.build_resource(
            "payments",
            {"OTEL_RESOURCE_ATTRIBUTES": "deployment.environment.name=test,service.version=1.2.3"},
        )
        self.tracer_provider = telemetry.create_tracer_provider(
            resource, SimpleSpanProcessor(self.spans)
        )
        self.reader = InMemoryMetricReader()
        self.meter_provider = telemetry.create_meter_provider(resource, [self.reader])

    def finished(self) -> list[ReadableSpan]:
        return list(self.spans.get_finished_spans())

    def span(self, name: str) -> ReadableSpan:
        matching = [span for span in self.finished() if span.name == name]
        if len(matching) != 1:
            raise AssertionError(f"{name}: {[span.name for span in self.finished()]}")
        return matching[0]

    def points(self, metric: str) -> list[tuple[dict[str, Any], Any]]:
        """(attributes, point) of ``metric``; unit and buckets checked by callers."""
        data = self.reader.get_metrics_data()
        found: list[tuple[dict[str, Any], Any]] = []
        for resource_metrics in data.resource_metrics if data else ():
            for scope in resource_metrics.scope_metrics:
                for item in scope.metrics:
                    if item.name == metric:
                        found.extend(
                            (dict(point.attributes or {}), point) for point in item.data.data_points
                        )
        return found

    def metric_units(self) -> dict[str, str]:
        data = self.reader.get_metrics_data()
        return {
            item.name: item.unit or ""
            for resource_metrics in (data.resource_metrics if data else ())
            for scope in resource_metrics.scope_metrics
            for item in scope.metrics
        }

    def close(self) -> None:
        self.tracer_provider.shutdown()
        self.meter_provider.shutdown()


@contextmanager
def global_propagator() -> Generator[None]:
    previous = propagate.get_global_textmap()
    propagate.set_global_textmap(create_propagator())
    try:
        yield
    finally:
        propagate.set_global_textmap(previous)


def _handlers(kit: PaymentsTestKit, use_cases: UseCases) -> PaymentsHandlers:
    executor = kit.executor()
    return PaymentsHandlers(
        get_payment_status=use_cases.instrument(
            names.GET_PAYMENT_STATUS, GetPaymentStatusHandler(kit.statuses, kit.permissions)
        ),
        create_payment=use_cases.instrument(
            names.CREATE_PAYMENT, CreatePaymentHandler(kit.payments, kit.permissions, kit.ids)
        ),
        authorize_payment=use_cases.instrument(
            names.AUTHORIZE_PAYMENT, AuthorizePaymentHandler(executor)
        ),
        capture_payment=use_cases.instrument(
            names.CAPTURE_PAYMENT, CapturePaymentHandler(executor)
        ),
        cancel_authorization=use_cases.instrument(
            names.CANCEL_AUTHORIZATION, CancelAuthorizationHandler(executor)
        ),
        refund_payment=use_cases.instrument(names.REFUND_PAYMENT, RefundPaymentHandler(executor)),
    )


@pytest.mark.usefixtures("database")
class GrpcPropagationTest(unittest.TestCase):
    """Caller context crosses the gRPC hop; spans and RPC metrics follow semconv RPC."""

    database: Database

    @override
    def setUp(self) -> None:
        self.signals = Signals()
        self.enterContext(global_propagator())
        self.kit = PaymentsTestKit(self.database)
        use_cases = UseCases(
            classify_payment_error,
            self.signals.tracer_provider,
            self.signals.meter_provider,
        )
        self.worker = GrpcWorker(
            ("127.0.0.1", 0),
            _handlers(self.kit, use_cases),
            StaticSubjectResolver(),
            ServerTelemetryInterceptor(self.signals.tracer_provider, self.signals.meter_provider),
        )
        self.worker.start()
        self.client_telemetry = ClientTelemetryInterceptor(
            "127.0.0.1", self.worker.port, self.signals.tracer_provider, self.signals.meter_provider
        )
        self.channel = grpc.intercept_channel(
            grpc.insecure_channel(f"127.0.0.1:{self.worker.port}"), self.client_telemetry
        )
        self.client = service_pb2_grpc.PaymentsServiceStub(self.channel)
        # Вызывающий без tracing: только metadata traceparent от «соседа».
        self.plain_channel = grpc.insecure_channel(f"127.0.0.1:{self.worker.port}")
        self.plain = service_pb2_grpc.PaymentsServiceStub(self.plain_channel)

    @override
    def tearDown(self) -> None:
        self.channel.close()
        self.plain_channel.close()
        self.worker.shutdown(time.monotonic() + 1)
        self.signals.close()

    def test_client_server_and_use_case_spans_form_one_trace(self) -> None:
        tracer = self.signals.tracer_provider.get_tracer("test")
        with tracer.start_as_current_span("caller", kind=SpanKind.INTERNAL) as caller:
            self.client.CreatePayment(
                service_pb2.CreatePaymentRequest(
                    order_id="order-1",
                    market_id="berlin",
                    amount=payments_pb2.Money(amount="25.00", currency="EUR"),
                )
            )
        method = "nomnom.payments.v1.PaymentsService/CreatePayment"
        spans = [span for span in self.signals.finished() if span.name == method]
        client = next(span for span in spans if span.kind is SpanKind.CLIENT)
        server = next(span for span in spans if span.kind is SpanKind.SERVER)
        use_case = self.signals.span("usecase create_payment")

        trace_id = caller.get_span_context().trace_id
        for span in (client, server, use_case):
            self.assertEqual(span.context.trace_id, trace_id)
        assert client.parent is not None and server.parent is not None
        assert use_case.parent is not None
        self.assertEqual(client.parent.span_id, caller.get_span_context().span_id)
        self.assertEqual(server.parent.span_id, client.context.span_id)
        self.assertTrue(server.parent.is_remote)
        self.assertEqual(use_case.parent.span_id, server.context.span_id)
        self.assertEqual(
            dict(server.attributes or {}),
            {
                "rpc.system.name": "grpc",
                "rpc.method": method,
                "rpc.response.status_code": "OK",
            },
        )
        self.assertEqual(
            dict(use_case.attributes or {}),
            {
                "nomnom.usecase.name": "create_payment",
                "nomnom.usecase.kind": "command",
                "nomnom.outcome": "success",
            },
        )

    def test_incoming_traceparent_is_continued(self) -> None:
        self.plain.GetPaymentStatus(
            service_pb2.GetPaymentStatusRequest(payment_id=self.kit.given_payment()),
            metadata=[("traceparent", CALLER_TRACEPARENT)],
        )
        server = next(
            span
            for span in self.signals.finished()
            if span.kind is SpanKind.SERVER and span.name.endswith("/GetPaymentStatus")
        )
        self.assertEqual(trace.format_trace_id(server.context.trace_id), CALLER_TRACE_ID)

    def test_rpc_metrics_carry_semconv_labels_and_code_names(self) -> None:
        tracer = self.signals.tracer_provider.get_tracer("test")
        with tracer.start_as_current_span("caller"), self.assertRaises(grpc.RpcError):
            self.client.GetPaymentStatus(service_pb2.GetPaymentStatusRequest(payment_id="missing"))
        method = "nomnom.payments.v1.PaymentsService/GetPaymentStatus"

        servers = self.signals.points("rpc.server.call.duration")
        self.assertEqual(
            [attributes for attributes, _ in servers],
            [
                {
                    "rpc.system.name": "grpc",
                    "rpc.method": method,
                    "rpc.response.status_code": "NOT_FOUND",
                }
            ],
        )
        self.assertEqual(servers[0][1].explicit_bounds, telemetry.SECONDS_BUCKETS)
        clients = self.signals.points("rpc.client.call.duration")
        self.assertEqual(
            [attributes for attributes, _ in clients],
            [
                {
                    "rpc.system.name": "grpc",
                    "rpc.method": method,
                    "rpc.response.status_code": "NOT_FOUND",
                    "error.type": "NOT_FOUND",
                    "server.address": "127.0.0.1",
                    "server.port": self.worker.port,
                }
            ],
        )
        units = self.signals.metric_units()
        self.assertEqual(units["rpc.server.call.duration"], "s")
        self.assertEqual(units["rpc.client.call.duration"], "s")
        # NOT_FOUND — ответ вызывающему, не сбой сервера; use case — rejected.
        server = next(s for s in self.signals.finished() if s.kind is SpanKind.SERVER)
        self.assertIs(server.status.status_code, StatusCode.UNSET)
        use_case = self.signals.span("usecase get_payment_status")
        self.assertEqual((use_case.attributes or {})["nomnom.outcome"], "rejected")
        self.assertEqual((use_case.attributes or {})["error.type"], "not_found")
        self.assertEqual(use_case.events, ())


class UseCaseTelemetryTest(unittest.TestCase):
    @override
    def setUp(self) -> None:
        self.signals = Signals()
        self.use_cases = UseCases(
            classify_payment_error, self.signals.tracer_provider, self.signals.meter_provider
        )

    @override
    def tearDown(self) -> None:
        self.signals.close()

    def test_outcomes_follow_the_usecase_registry(self) -> None:
        class Raising:
            def __init__(self, error: Exception) -> None:
                self.error = error

            def handle(self, request: str, /) -> str:
                raise self.error

        cases: list[Exception] = [
            ProviderUnavailableError("provider down: token=abc"),
            RuntimeError("password=secret"),
        ]
        for error in cases:
            handler = self.use_cases.instrument(names.AUTHORIZE_PAYMENT, Raising(error))
            with self.assertRaises(type(error)):
                handler.handle("request")

        got = {
            (attrs["nomnom.outcome"], attrs["error.type"])
            for attrs, point in self.signals.points("nomnom.usecase.duration")
        }
        self.assertEqual(got, {("error", "provider_unavailable"), ("error", "internal")})
        for attrs, point in self.signals.points("nomnom.usecase.duration"):
            self.assertEqual(attrs["nomnom.usecase.name"], "authorize_payment")
            self.assertEqual(attrs["nomnom.usecase.kind"], "command")
            self.assertEqual(point.explicit_bounds, telemetry.SECONDS_BUCKETS)
        for span in self.signals.finished():
            self.assertIs(span.status.status_code, StatusCode.ERROR)
            # Текст ошибки (может содержать вход) не попадает в span.
            self.assertEqual(span.events, ())
            self.assertNotIn("secret", str(span.status.description))


class ProviderTelemetryTest(unittest.TestCase):
    def test_duration_by_operation_and_outcome(self) -> None:
        signals = Signals()
        self.addCleanup(signals.close)
        provider = ObservedProvider(
            FakePaymentProvider(), signals.tracer_provider, signals.meter_provider
        )

        def request(kind: OperationKind, amount: str, reference: str | None) -> None:
            provider.execute(
                ProviderOperationRequest(
                    OperationId(f"op-{kind.value}-{amount}"),
                    PaymentId("payment-1"),
                    kind,
                    f"key-{kind.value}-{amount}",
                    Money(Decimal(amount), "EUR"),
                    reference,
                )
            )

        # Provider вызывается внутри use case: корневые client spans не сэмплируются.
        with signals.tracer_provider.get_tracer("t").start_as_current_span("usecase x"):
            request(OperationKind.AUTHORIZE, "25.00", None)
            request(OperationKind.AUTHORIZE, "666.00", None)
            request(OperationKind.CANCEL_AUTHORIZATION, "25.00", "auth-ref")

        got = sorted(
            (attrs["nomnom.payment.operation"], attrs["nomnom.outcome"])
            for attrs, _ in signals.points("nomnom.payments.provider.request.duration")
        )
        self.assertEqual(
            got, [("authorize", "declined"), ("authorize", "success"), ("cancel", "success")]
        )
        self.assertEqual(signals.metric_units()["nomnom.payments.provider.request.duration"], "s")
        self.assertEqual(
            sorted(span.name for span in signals.finished()),
            [
                "payments.provider authorize",
                "payments.provider authorize",
                "payments.provider cancel",
                "usecase x",
            ],
        )


class OutboxTraceparentTest(unittest.TestCase):
    """The Outbox stores the write-time ``traceparent``; Debezium copies it to the header."""

    def test_outbox_stores_the_context_of_the_write(self) -> None:
        signals = Signals()
        self.addCleanup(signals.close)
        tracer = signals.tracer_provider.get_tracer("test")
        with tracer.start_as_current_span("usecase capture_payment") as written:
            traceparent = outbox_traceparent()
        context = propagate.extract({"traceparent": traceparent})
        self.assertEqual(
            trace.get_current_span(context).get_span_context().span_id,
            written.get_span_context().span_id,
        )

    def test_without_a_span_the_traceparent_is_empty(self) -> None:
        # Колонка NOT NULL: Debezium требует поле, пустая строка — «без контекста».
        self.assertEqual(outbox_traceparent(), "")


class BaggageAllowlistTest(unittest.TestCase):
    def test_only_allowlisted_keys_cross_the_process(self) -> None:
        propagator = create_propagator({"tenant"})
        context = propagator.extract({"baggage": "tenant=t1,user.email=a%40b.c"})
        self.assertEqual(baggage.get_all(context), {"tenant": "t1"})
        carrier: dict[str, str] = {}
        token = attach(baggage.set_baggage("card", "4111", context))
        try:
            propagator.inject(carrier)
        finally:
            detach(token)
        self.assertEqual(carrier.get("baggage"), "tenant=t1")

    def test_default_drops_baggage_and_malformed_traceparent(self) -> None:
        context = create_propagator().extract(
            {"baggage": "user=1", "traceparent": "00-zzz-00f067aa0ba902b7-01"}
        )
        self.assertEqual(baggage.get_all(context), {})
        self.assertFalse(trace.get_current_span(context).get_span_context().is_valid)


class JsonLogTest(unittest.TestCase):
    @override
    def setUp(self) -> None:
        root = logging.getLogger()
        saved = (list(root.handlers), root.level)

        def restore() -> None:
            for handler in list(root.handlers):
                root.removeHandler(handler)
            for handler in saved[0]:
                root.addHandler(handler)
            root.setLevel(saved[1])

        self.addCleanup(restore)
        telemetry.configure_process_logging(
            "payments",
            {
                "OTEL_RESOURCE_ATTRIBUTES": (
                    "deployment.environment.name=test,service.version=1.2.3,"
                    "service.instance.id=payments-7d9f"
                )
            },
        )

    def lines(self, emit: Any) -> list[dict[str, Any]]:
        out = io.StringIO()
        with redirect_stdout(out):
            emit()
        return [json.loads(line) for line in out.getvalue().splitlines()]

    def test_record_schema_and_trace_context(self) -> None:
        signals = Signals()
        self.addCleanup(signals.close)
        log = logging.getLogger("grpc._server")
        spans: list[trace.Span] = []

        def emit() -> None:
            log.info("outside")
            with signals.tracer_provider.get_tracer("t").start_as_current_span("rpc") as span:
                spans.append(span)
                log.warning("inside", extra={"operation": "capture_payment", "subject": "u"})

        outside, inside = self.lines(emit)
        self.assertEqual(
            outside.keys(),
            {
                "timestamp",
                "severity",
                "message",
                "service.namespace",
                "service.name",
                "service.version",
                "deployment.environment.name",
                "service.instance.id",
                "logger",
            },
        )
        self.assertTrue(outside["timestamp"].endswith("Z"))
        datetime.fromisoformat(outside["timestamp"])
        self.assertEqual(outside["severity"], "INFO")
        self.assertEqual(outside["service.namespace"], "nomnom")
        self.assertEqual(outside["service.name"], "payments")
        self.assertEqual(outside["deployment.environment.name"], "test")
        self.assertEqual(outside["logger"], "grpc._server")
        self.assertNotIn("trace_id", outside)

        context = spans[0].get_span_context()
        self.assertEqual(inside["severity"], "WARN")
        self.assertEqual(inside["trace_id"], trace.format_trace_id(context.trace_id))
        self.assertEqual(inside["span_id"], trace.format_span_id(context.span_id))
        self.assertEqual(inside["operation"], "capture_payment")
        # Поле вне allowlist (subject) не пишется.
        self.assertNotIn("subject", inside)

    def test_exception_is_structured_and_secrets_are_redacted(self) -> None:
        def emit() -> None:
            try:
                raise ValueError("connect postgresql://payments:hunter2@db/x token=abc.def")
            except ValueError:
                logging.getLogger("pkg.lifecycle").exception(
                    "call with Authorization: Bearer eyJhbGciOi.eyJzdWIiOiJ4.sig card 4111 1111 1111 1111"
                )

        (line,) = self.lines(emit)
        text = json.dumps(line)
        for secret in ("hunter2", "abc.def", "eyJhbGciOi", "4111 1111 1111 1111"):
            self.assertNotIn(secret, text)
        self.assertEqual(line["severity"], "ERROR")
        self.assertEqual(line["exception"]["type"], "builtins.ValueError")
        self.assertIn("[REDACTED]", line["exception"]["message"])
        self.assertIn("Traceback", line["exception"]["stacktrace"])

    def test_redaction_keeps_ordinary_numbers(self) -> None:
        self.assertEqual(telemetry.redact("took 1761393600000 ms"), "took 1761393600000 ms")


class RuntimeAndResourceTest(unittest.TestCase):
    @unittest.skipUnless(sys.platform == "linux", "process metrics read /proc")
    def test_runtime_metrics_and_shared_resource(self) -> None:
        signals = Signals()
        self.addCleanup(signals.close)
        cpu = signals.points("process.cpu.time")
        self.assertEqual({attrs["cpu.mode"] for attrs, _ in cpu}, {"user", "system"})
        gc_points = signals.points("cpython.gc.collections")
        self.assertEqual({attrs["cpython.gc.generation"] for attrs, _ in gc_points}, {0, 1, 2})
        units = signals.metric_units()
        self.assertEqual(units["process.cpu.time"], "s")
        self.assertEqual(units["process.memory.usage"], "By")
        self.assertIn("process.thread.count", units)
        self.assertFalse([name for name in units if name.startswith("system.")])

        resource = signals.meter_provider._sdk_config.resource.attributes
        self.assertEqual(resource["service.namespace"], "nomnom")
        self.assertEqual(resource["service.name"], "payments")
        self.assertEqual(resource["service.version"], "1.2.3")
        self.assertEqual(signals.tracer_provider.resource.attributes, resource)

    def test_service_name_override(self) -> None:
        resource = telemetry.build_resource("payments", {"OTEL_SERVICE_NAME": "canary"})
        self.assertEqual(resource.attributes["service.name"], "canary")
        self.assertEqual(resource.attributes["service.namespace"], "nomnom")

    def test_orphan_client_spans_are_not_sampled(self) -> None:
        signals = Signals()
        self.addCleanup(signals.close)
        tracer = signals.tracer_provider.get_tracer("t")
        with tracer.start_as_current_span("SELECT", kind=SpanKind.CLIENT):
            pass
        with (
            tracer.start_as_current_span("usecase x"),
            tracer.start_as_current_span("SELECT", kind=SpanKind.CLIENT),
        ):
            pass
        self.assertEqual(sorted(s.name for s in signals.finished()), ["SELECT", "usecase x"])


@pytest.mark.usefixtures("database")
class WebhookSpanTest(unittest.TestCase):
    database: Database

    def test_webhook_gets_a_server_span_with_the_route(self) -> None:
        signals = Signals()
        self.addCleanup(signals.close)
        kit = PaymentsTestKit(self.database)
        kit.given_payment(PaymentState.AUTHORIZED)
        handler = create_webhook_handler(
            WebhookVerifier("whsec", timedelta(minutes=5)),
            SANDBOX_WEBHOOK_FORMAT,
            ApplyProviderCallbackHandler(kit.uow, kit.inbox, kit.applier()),
            lambda: datetime(2026, 10, 25, tzinfo=UTC),
            signals.tracer_provider,
        )
        worker = HttpWorker(("127.0.0.1", 0), handler, name="webhooks")
        worker.start()
        thread = threading.Thread(target=worker.run, daemon=True)
        thread.start()
        try:
            import http.client

            connection = http.client.HTTPConnection("127.0.0.1", worker.port, timeout=5)
            connection.request(
                "POST",
                WEBHOOK_PATH,
                body=b"{}",
                headers={"Provider-Signature": "bad", "traceparent": CALLER_TRACEPARENT},
            )
            self.assertEqual(connection.getresponse().status, 400)
            connection.close()
        finally:
            worker.shutdown(time.monotonic() + 2)
            thread.join(2)
        span = signals.span(f"POST {WEBHOOK_PATH}")
        self.assertIs(span.kind, SpanKind.SERVER)
        self.assertEqual((span.attributes or {})["http.route"], WEBHOOK_PATH)
        self.assertEqual((span.attributes or {})["http.response.status_code"], 400)
        # Trace провайдера не продолжается.
        self.assertIsNone(span.parent)
        self.assertNotEqual(trace.format_trace_id(span.context.trace_id), CALLER_TRACE_ID)


class SetupTest(unittest.TestCase):
    def test_signal_endpoints_are_independent(self) -> None:
        owner = telemetry.setup(
            environ={"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://127.0.0.1:1"}
        )
        try:
            self.assertTrue(owner.enabled)
            self.assertEqual(owner.resource.attributes["service.name"], "payments")
        finally:
            owner.shutdown(5.0)


if __name__ == "__main__":
    unittest.main()
