"""Mapping of Stripe webhook events to provider callbacks.

Stripe signs with the scheme of ``WebhookVerifier``, only the header differs. An event finds
its operation by ``metadata.operation_id``, which the gateway sets on every request. Cancel
Authorization cannot update the metadata and is answered synchronously, so
``payment_intent.canceled`` is not mapped. Other events and objects created outside this
service (no ``operation_id``) are ignored with 200, otherwise Stripe would redeliver them.
"""

from slices.payment.applications.apply_provider_callback import ApplyProviderCallbackInput
from slices.payment.applications.ports import ProviderOperationOutcome

from .webhook import InvalidWebhookError, WebhookFormat, parse_body

STRIPE_SIGNATURE_HEADER = "Stripe-Signature"


def parse_stripe_event(body: bytes) -> ApplyProviderCallbackInput | None:
    """Map a verified event; ``None`` means the event says nothing about an operation."""
    payload = parse_body(body)
    message_id = payload.get("id")
    event_type = payload.get("type")
    data = payload.get("data")
    if not isinstance(message_id, str) or not isinstance(event_type, str):
        raise InvalidWebhookError("stripe event id and type are required")
    if not isinstance(data, dict) or not isinstance(data.get("object"), dict):
        raise InvalidWebhookError("stripe event object is required")
    event_object = data["object"]
    outcome = _outcome(event_type, event_object)
    if outcome is None:
        return None
    metadata = event_object.get("metadata")
    operation_id = metadata.get("operation_id") if isinstance(metadata, dict) else None
    if not isinstance(operation_id, str):
        return None
    return ApplyProviderCallbackInput(message_id, operation_id, outcome)


def _outcome(event_type: str, event_object: dict[str, object]) -> ProviderOperationOutcome | None:
    reference = event_object.get("id")
    if not isinstance(reference, str):
        raise InvalidWebhookError("stripe event object id is required")
    if event_type in {"payment_intent.amount_capturable_updated", "payment_intent.succeeded"}:
        return ProviderOperationOutcome.succeeded(reference)
    if event_type == "payment_intent.payment_failed":
        error = event_object.get("last_payment_error")
        code = error.get("code") if isinstance(error, dict) else None
        return ProviderOperationOutcome.declined(code if isinstance(code, str) else "")
    if event_type in {"refund.updated", "refund.failed"}:
        status = event_object.get("status")
        if status == "succeeded":
            return ProviderOperationOutcome.succeeded(reference)
        if status == "failed":
            reason = event_object.get("failure_reason")
            return ProviderOperationOutcome.declined(reason if isinstance(reason, str) else "")
    return None


STRIPE_WEBHOOK_FORMAT = WebhookFormat(STRIPE_SIGNATURE_HEADER, parse_stripe_event)
