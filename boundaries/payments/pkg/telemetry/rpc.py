"""gRPC server and client telemetry: W3C propagation in metadata, spans and RPC metrics.

Current semconv RPC (the inventory ``rpc`` section): spans are named ``Service/Method``
and carry ``rpc.system.name=grpc``, ``rpc.method`` and ``rpc.response.status_code``
(the code name, e.g. ``NOT_FOUND``); histograms ``rpc.server.call.duration`` and
``rpc.client.call.duration`` are in seconds with explicit seconds buckets.

One interceptor per direction instruments a call, so spans and metrics never come from
two competing middlewares. ``opentelemetry-instrumentation-grpc`` is not used: it emits
no RPC metrics and still writes the pre-1.40 attributes (``rpc.system``,
``rpc.grpc.status_code``).

Only unary-unary methods are instrumented: ``nomnom.payments.v1`` and the SpiceDB
``CheckPermission`` calls are unary. Error messages never reach telemetry.
"""

import collections
import time
from collections.abc import Callable
from typing import Any, cast, override

import grpc
from opentelemetry import metrics, propagate, trace
from opentelemetry.trace import SpanKind, Status, StatusCode

from .buckets import SECONDS_BUCKETS
from .propagation import metadata_carrier

INSTRUMENTATION = "nomnom.payments.grpc"

RPC_SYSTEM_NAME = "rpc.system.name"
RPC_METHOD = "rpc.method"
RPC_RESPONSE_STATUS_CODE = "rpc.response.status_code"
ERROR_TYPE = "error.type"
SERVER_ADDRESS = "server.address"
SERVER_PORT = "server.port"

# Server-side codes that are failures of the server (semconv RPC spans); the others are
# answers to the caller, e.g. NOT_FOUND or PERMISSION_DENIED.
SERVER_ERROR_CODES = frozenset(
    {
        grpc.StatusCode.UNKNOWN,
        grpc.StatusCode.DEADLINE_EXCEEDED,
        grpc.StatusCode.UNIMPLEMENTED,
        grpc.StatusCode.INTERNAL,
        grpc.StatusCode.UNAVAILABLE,
        grpc.StatusCode.DATA_LOSS,
    }
)


def _method_name(method: str | bytes) -> str:
    """``/pkg.Service/Method`` → ``pkg.Service/Method``."""
    text = method.decode() if isinstance(method, bytes) else method
    return text.lstrip("/")


def _context_code(context: grpc.ServicerContext) -> grpc.StatusCode | None:
    """Code set by ``abort``/``set_code``; ``None`` while the call is still OK."""
    code = getattr(context, "code", None)
    if not callable(code):
        return None
    value = code()
    return value if isinstance(value, grpc.StatusCode) else None


class ServerTelemetryInterceptor(grpc.ServerInterceptor):
    """Server span and ``rpc.server.call.duration`` of every unary call.

    The caller's ``traceparent`` is extracted from the invocation metadata; a malformed
    one starts a new trace. Providers default to the global ones (no-op until setup).
    """

    def __init__(
        self,
        tracer_provider: trace.TracerProvider | None = None,
        meter_provider: metrics.MeterProvider | None = None,
    ) -> None:
        self._tracer = trace.get_tracer(INSTRUMENTATION, tracer_provider=tracer_provider)
        meter = metrics.get_meter(INSTRUMENTATION, meter_provider=meter_provider)
        self._duration = meter.create_histogram(
            "rpc.server.call.duration",
            unit="s",
            description="Measures the duration of inbound remote procedure calls.",
            explicit_bucket_boundaries_advisory=list(SECONDS_BUCKETS),
        )

    @override
    def intercept_service(
        self,
        continuation: Callable[[grpc.HandlerCallDetails], grpc.RpcMethodHandler | None],
        handler_call_details: grpc.HandlerCallDetails,
    ) -> grpc.RpcMethodHandler | None:
        found = continuation(handler_call_details)
        # Неизвестный метод (UNIMPLEMENTED) и streaming не инструментируются: имя метода
        # от клиента не становится label.
        handler: Any = found
        if found is None or handler.unary_unary is None:
            return found
        method = _method_name(handler_call_details.method)
        behavior = handler.unary_unary

        def observed(request: Any, context: grpc.ServicerContext) -> Any:
            return self._observe(method, behavior, request, context)

        return cast(
            grpc.RpcMethodHandler,
            grpc.unary_unary_rpc_method_handler(
                observed,
                request_deserializer=handler.request_deserializer,
                response_serializer=handler.response_serializer,
            ),
        )

    def _observe(
        self,
        method: str,
        behavior: Callable[[Any, grpc.ServicerContext], Any],
        request: Any,
        context: grpc.ServicerContext,
    ) -> Any:
        parent = propagate.extract(metadata_carrier(context.invocation_metadata()))
        attributes = {RPC_SYSTEM_NAME: "grpc", RPC_METHOD: method}
        started = time.perf_counter()
        code = grpc.StatusCode.OK
        with self._tracer.start_as_current_span(
            method,
            context=parent,
            kind=SpanKind.SERVER,
            attributes=attributes,
            record_exception=False,
            set_status_on_exception=False,
        ) as span:
            try:
                response = behavior(request, context)
                code = _context_code(context) or grpc.StatusCode.OK
                return response
            except BaseException:
                # context.abort бросает исключение после установки кода; иное исключение
                # grpcio превращает в UNKNOWN.
                code = _context_code(context) or grpc.StatusCode.UNKNOWN
                raise
            finally:
                span.set_attribute(RPC_RESPONSE_STATUS_CODE, code.name)
                if code in SERVER_ERROR_CODES:
                    span.set_attribute(ERROR_TYPE, code.name)
                    span.set_status(Status(StatusCode.ERROR, code.name))
                self._duration.record(
                    time.perf_counter() - started,
                    {**attributes, RPC_RESPONSE_STATUS_CODE: code.name},
                )


class _CallDetails(
    collections.namedtuple(
        "_CallDetails",
        ["method", "timeout", "metadata", "credentials", "wait_for_ready", "compression"],
    ),
    grpc.ClientCallDetails,
):
    """Client call details with the injected trace context metadata."""


class ClientTelemetryInterceptor(grpc.UnaryUnaryClientInterceptor):
    """Client span, W3C context injection and ``rpc.client.call.duration`` of unary calls.

    Every non-OK code is a failure of the call (``error.type`` = code name).
    """

    def __init__(
        self,
        server_address: str,
        server_port: int | None,
        tracer_provider: trace.TracerProvider | None = None,
        meter_provider: metrics.MeterProvider | None = None,
    ) -> None:
        self._server: dict[str, str | int] = {SERVER_ADDRESS: server_address}
        if server_port is not None:
            self._server[SERVER_PORT] = server_port
        self._tracer = trace.get_tracer(INSTRUMENTATION, tracer_provider=tracer_provider)
        meter = metrics.get_meter(INSTRUMENTATION, meter_provider=meter_provider)
        self._duration = meter.create_histogram(
            "rpc.client.call.duration",
            unit="s",
            description="Measures the duration of outbound remote procedure calls.",
            explicit_bucket_boundaries_advisory=list(SECONDS_BUCKETS),
        )

    @override
    def intercept_unary_unary(
        self,
        continuation: Callable[[grpc.ClientCallDetails, Any], Any],
        client_call_details: grpc.ClientCallDetails,
        request: Any,
    ) -> Any:
        # grpc.ClientCallDetails объявляет поля только в документации, не в типах.
        call_details: Any = client_call_details
        method = _method_name(call_details.method)
        attributes: dict[str, str | int] = {
            RPC_SYSTEM_NAME: "grpc",
            RPC_METHOD: method,
            **self._server,
        }
        code = grpc.StatusCode.UNKNOWN
        started = time.perf_counter()
        with self._tracer.start_as_current_span(
            method,
            kind=SpanKind.CLIENT,
            attributes=attributes,
            record_exception=False,
            set_status_on_exception=False,
        ) as span:
            try:
                carrier: dict[str, str] = {}
                propagate.inject(carrier)
                metadata = [*(call_details.metadata or ()), *carrier.items()]
                details = _CallDetails(
                    call_details.method,
                    call_details.timeout,
                    metadata,
                    call_details.credentials,
                    getattr(call_details, "wait_for_ready", None),
                    getattr(call_details, "compression", None),
                )
                call = continuation(details, request)
                # Unary: code() ждёт завершения вызова; ошибка поднимется у вызывающего
                # при result().
                code = call.code() or grpc.StatusCode.UNKNOWN
                return call
            except grpc.RpcError as error:
                code_of = getattr(error, "code", None)
                code = (code_of() if callable(code_of) else None) or grpc.StatusCode.UNKNOWN
                raise
            finally:
                outcome: dict[str, str | int] = {RPC_RESPONSE_STATUS_CODE: code.name}
                if code is not grpc.StatusCode.OK:
                    outcome[ERROR_TYPE] = code.name
                    span.set_status(Status(StatusCode.ERROR, code.name))
                span.set_attributes(outcome)
                self._duration.record(time.perf_counter() - started, {**attributes, **outcome})


def split_target(target: str) -> tuple[str, int | None]:
    """``host:port`` of a gRPC target as ``server.address`` and ``server.port``."""
    host, sep, port = target.rpartition(":")
    if not sep or not port.isdigit():
        return target, None
    return host.strip("[]"), int(port)
