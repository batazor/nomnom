"""HTTP endpoint of provider webhooks: verify, map, apply through the use case.

The worker serving it is assembled by ``di.webhooks``.

A non-2xx answer makes the provider redeliver; permanent rejections are 4xx.
"""

import json
import logging
from collections.abc import Callable
from datetime import datetime
from http.server import BaseHTTPRequestHandler

from slices.payment.applications.apply_provider_callback import ApplyProviderCallbackHandler
from slices.payment.applications.errors import (
    InvalidPaymentRequestError,
    PaymentNotFoundError,
    PaymentOperationNotFoundError,
    PaymentStateConflictError,
    ProviderResultConflictError,
)

from .webhook import SIGNATURE_HEADER, InvalidWebhookError, WebhookVerifier, parse_callback

logger = logging.getLogger(__name__)

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
    callbacks: ApplyProviderCallbackHandler,
    now: Callable[[], datetime],
) -> type[BaseHTTPRequestHandler]:
    class WebhookHandler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def do_POST(self) -> None:
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
                verifier.verify(self.headers.get(SIGNATURE_HEADER), body, now())
                result = callbacks.handle(parse_callback(body))
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
            data = json.dumps(payload).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def log_message(self, format: str, *args: object) -> None:
            logger.debug(format, *args)

    return WebhookHandler
