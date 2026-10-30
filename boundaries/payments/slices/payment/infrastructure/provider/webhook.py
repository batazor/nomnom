"""Authenticity check and mapping of provider webhooks.

Signature header: ``t=<unix seconds>,v1=<hex HMAC-SHA256 of "<t>.<raw body>">``. The
timestamp bounds replays; deduplication of a replay inside the window is the inbox's job.
Body: ``{"id": <message id>, "operation_id": ..., "status": "succeeded"|"declined",
"provider_reference": ..., "decline_reason": ...}``.
"""

import hashlib
import hmac
import json
from collections.abc import Callable
from dataclasses import dataclass
from datetime import datetime, timedelta

from slices.payment.applications.apply_provider_callback import ApplyProviderCallbackInput
from slices.payment.applications.ports import ProviderOperationOutcome

SIGNATURE_HEADER = "Provider-Signature"


class InvalidWebhookError(ValueError):
    """The webhook is not authentic or its payload is malformed; answer 400, never apply."""


def sign_payload(secret: str, timestamp: int, body: bytes) -> str:
    """Return the ``Provider-Signature`` header value of ``body`` signed at ``timestamp``."""
    digest = hmac.new(secret.encode(), f"{timestamp}.".encode() + body, hashlib.sha256)
    return f"t={timestamp},v1={digest.hexdigest()}"


class WebhookVerifier:
    """Verifies webhook signatures with the shared secret; the secret is required."""

    def __init__(self, secret: str, tolerance: timedelta) -> None:
        if not secret:
            raise ValueError("PROVIDER__WEBHOOK_SECRET is required")
        self._secret = secret
        self._tolerance = tolerance

    def verify(self, header: str | None, body: bytes, now: datetime) -> None:
        """Accept the webhook only with a valid signature inside the timestamp tolerance.

        Raises:
            InvalidWebhookError: malformed header, stale timestamp or
                signature mismatch.
        """
        timestamp, signatures = _parse_header(header or "")
        if abs(now.timestamp() - timestamp) > self._tolerance.total_seconds():
            raise InvalidWebhookError("webhook timestamp is outside the tolerance")
        expected = sign_payload(self._secret, timestamp, body).split("v1=", 1)[1]
        if not any(hmac.compare_digest(expected, candidate) for candidate in signatures):
            raise InvalidWebhookError("webhook signature mismatch")


def _parse_header(header: str) -> tuple[int, list[str]]:
    timestamp: int | None = None
    signatures: list[str] = []
    for part in header.split(","):
        name, _, value = part.strip().partition("=")
        if name == "t" and value.isdigit():
            timestamp = int(value)
        elif name == "v1" and value:
            signatures.append(value)
    if timestamp is None or not signatures:
        raise InvalidWebhookError("malformed signature header")
    return timestamp, signatures


def parse_body(body: bytes) -> dict[str, object]:
    """Decode a webhook body, which is a JSON object in every format."""
    try:
        payload = json.loads(body)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise InvalidWebhookError("webhook body is not JSON") from error
    if not isinstance(payload, dict):
        raise InvalidWebhookError("webhook body must be an object")
    return payload


def parse_callback(body: bytes) -> ApplyProviderCallbackInput:
    """Map a verified body; call only after ``WebhookVerifier.verify``."""
    payload = parse_body(body)
    message_id = payload.get("id")
    operation_id = payload.get("operation_id")
    if not isinstance(message_id, str) or not isinstance(operation_id, str):
        raise InvalidWebhookError("webhook id and operation_id are required")
    status = payload.get("status")
    if status == "succeeded":
        reference = payload.get("provider_reference")
        outcome = ProviderOperationOutcome.succeeded(
            reference if isinstance(reference, str) else None
        )
    elif status == "declined":
        reason = payload.get("decline_reason")
        outcome = ProviderOperationOutcome.declined(reason if isinstance(reason, str) else "")
    else:
        raise InvalidWebhookError("webhook status must be succeeded or declined")
    return ApplyProviderCallbackInput(message_id, operation_id, outcome)


@dataclass(frozen=True)
class WebhookFormat:
    """Wire format of one provider: its signature header and body mapping.

    ``parse`` returns ``None`` for a message that says nothing about an operation.
    """

    signature_header: str
    parse: Callable[[bytes], ApplyProviderCallbackInput | None]


SANDBOX_WEBHOOK_FORMAT = WebhookFormat(SIGNATURE_HEADER, parse_callback)
