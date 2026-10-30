"""HTTP endpoint of provider webhooks: verify, map, apply through the use case.

The worker serving it is assembled by ``di.payments``.

A non-2xx answer makes the provider redeliver; permanent rejections are 4xx.

Every request gets a server span ``POST /webhooks/provider`` (``http.route`` is the
fixed path, never the raw URL). The Provider is an external party: its trace headers
are not trusted, each webhook starts a new trace. Headers, signature and body never
reach telemetry.
"""

import json
import logging
from collections.abc import Callable
from datetime import datetime
from http.server import BaseHTTPRequestHandler

from opentelemetry import trace
from opentelemetry.context import Context
from opentelemetry.trace import SpanKind, Status, StatusCode

from pkg.usecase import Handler
from slices.payment.applications.apply_provider_callback import (
    ApplyProviderCallbackInput,
    ApplyProviderCallbackResult,
)
from slices.payment.applications.errors import (
    InvalidPaymentRequestError,
    PaymentNotFoundError,
    PaymentOperationNotFoundError,
    PaymentStateConflictError,
    ProviderResultConflictError,
)

from .webhook import InvalidWebhookError, WebhookFormat, WebhookVerifier

logger = logging.getLogger(__name__)

INSTRUMENTATION = "nomnom.payments.webhooks"

WEBHOOK_PATH = "/webhooks/provider"
MAX_BODY_BYTES = 64 * 1024

# Ordered: the first matching error decides the status code.
_ERROR_STATUS: tuple[tuple[type[Exception], int], ...] = (
    (InvalidWebhookError, 400),
    (InvalidPaymentRequestError, 400),
    (PaymentOperationNotFoundError, 404),
    (PaymentNotFoundError, 404),
    (ProviderResultConflictError, 409),
    (PaymentStateConflictError, 409),
)


def create_webhook_handler(
    verifier: WebhookVerifier,
    webhook_format: WebhookFormat,
    callbacks: Handler[ApplyProviderCallbackInput, ApplyProviderCallbackResult],
    now: Callable[[], datetime],
    tracer_provider: trace.TracerProvider | None = None,
) -> type[BaseHTTPRequestHandler]:
    """Build the ``BaseHTTPRequestHandler`` class serving ``POST /webhooks/provider``.

    The handler verifies the signature, maps the body and applies it through ``callbacks``;
    application errors map to 4xx so that only technical failures make the provider redeliver.
    A message without an operation is answered 200 and applies nothing.
    """
    tracer = trace.get_tracer(INSTRUMENTATION, tracer_provider=tracer_provider)

    class WebhookHandler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"
        _status = 0

        def do_POST(self) -> None:
            known = self.path == WEBHOOK_PATH
            attributes = {"http.request.method": "POST", "url.scheme": "http"}
            if known:
                attributes["http.route"] = WEBHOOK_PATH
            with tracer.start_as_current_span(
                f"POST {WEBHOOK_PATH}" if known else "POST",
                # Новый trace: trace context внешнего провайдера не продолжается.
                context=Context(),
                kind=SpanKind.SERVER,
                attributes=attributes,
                record_exception=False,
                set_status_on_exception=False,
            ) as span:
                try:
                    self._serve()
                finally:
                    span.set_attribute("http.response.status_code", self._status)
                    if self._status >= 500 or self._status == 0:
                        error_type = str(self._status) if self._status else "_OTHER"
                        span.set_attribute("error.type", error_type)
                        span.set_status(Status(StatusCode.ERROR, error_type))

        def _serve(self) -> None:
            if self.path != WEBHOOK_PATH:
                self._reply(404, {"error": "not found"})
                return
            try:
                length = int(self.headers.get("Content-Length", ""))
            except ValueError:
                self._reply(411, {"error": "content length required"})
                return
            if length < 0 or length > MAX_BODY_BYTES:
                self._reply(413, {"error": "body too large"})
                return
            body = self.rfile.read(length)
            try:
                verifier.verify(self.headers.get(webhook_format.signature_header), body, now())
                command = webhook_format.parse(body)
                if command is None:
                    self._reply(200, {"status": "ignored"})
                    return
                result = callbacks.handle(command)
            except Exception as error:
                for error_type, status in _ERROR_STATUS:
                    if isinstance(error, error_type):
                        self._reply(status, {"error": str(error)})
                        return
                logger.warning("provider webhook failed", exc_info=True)
                self._reply(500, {"error": "internal error"})
                return
            self._reply(200, {"status": result.status.value})

        def _reply(self, status: int, payload: dict[str, str]) -> None:
            self._status = status
            data = json.dumps(payload).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def log_message(self, format: str, *args: object) -> None:
            logger.debug(format, *args)

    return WebhookHandler
