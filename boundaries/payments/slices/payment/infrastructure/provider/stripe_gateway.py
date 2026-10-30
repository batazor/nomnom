"""Stripe test mode behind the provider gateway: a real provider API, no real money.

Authorization is a confirmed PaymentIntent with manual capture, Capture and Cancel
Authorization act on it, Refund creates a Refund of the whole captured amount. The domain has
no card, so a server-side test PaymentMethod stands in for it; the Authorization of
``DECLINE_AMOUNT`` uses the declining test card, as ``FakePaymentProvider`` declines it.

Errors that repeating cannot fix end the operation as a decline; the rest stay retryable.
"""

import logging
from decimal import Decimal
from typing import Protocol

import stripe

from slices.payment.applications.ports import (
    ProviderOperationOutcome,
    ProviderOperationRequest,
    ProviderUnavailableError,
)
from slices.payment.domains.payment import Money, OperationKind

from .fake import DECLINE_AMOUNT

logger = logging.getLogger(__name__)

APPROVED_PAYMENT_METHOD = "pm_card_visa"
DECLINED_PAYMENT_METHOD = "pm_card_visa_chargeDeclined"
# Currencies with two decimal places: their minor unit is a hundredth. Stripe counts other
# currencies differently, so they are refused instead of being charged a wrong amount.
SUPPORTED_CURRENCIES = frozenset({"EUR", "GBP", "USD"})


class _StripeObject(Protocol):
    id: str
    status: str | None


def create_stripe_client(api_key: str, timeout_seconds: float) -> stripe.StripeClient:
    """Build the client; Temporal repeats failed operations, so the SDK does not retry."""
    return stripe.StripeClient(
        api_key,
        http_client=stripe.new_default_http_client(timeout=timeout_seconds),
        max_network_retries=0,
    )


class StripePaymentProvider:
    """Executes operations in Stripe; the idempotency key deduplicates repeated requests."""

    def __init__(self, client: stripe.StripeClient) -> None:
        self._client = client

    def execute(self, request: ProviderOperationRequest) -> ProviderOperationOutcome:
        """Return the Stripe answer as a provider outcome.

        Raises:
            ProviderUnavailableError: Stripe could not be reached or the failure is technical;
                retry with the same key.
        """
        try:
            return self._execute(request)
        except stripe.CardError as error:
            return ProviderOperationOutcome.declined(str(error.code))
        except stripe.InvalidRequestError as error:
            logger.warning("stripe rejected the request: %s", error.user_message)
            return ProviderOperationOutcome.declined("stripe_invalid_request")
        except stripe.StripeError as error:
            raise ProviderUnavailableError(
                f"stripe request failed: {type(error).__name__}"
            ) from error

    def _execute(self, request: ProviderOperationRequest) -> ProviderOperationOutcome:
        if request.kind is OperationKind.AUTHORIZE:
            return self._authorize(request)
        reference = request.authorization_reference
        if not reference:
            return ProviderOperationOutcome.declined("authorization_not_found")
        options: stripe.RequestOptions = {"idempotency_key": request.idempotency_key}
        if request.kind is OperationKind.CAPTURE:
            intent = self._client.v1.payment_intents.capture(
                reference, {"metadata": _metadata(request)}, options
            )
            return _outcome(intent, "succeeded")
        if request.kind is OperationKind.CANCEL_AUTHORIZATION:
            return _outcome(
                self._client.v1.payment_intents.cancel(reference, options=options), "canceled"
            )
        if request.kind is OperationKind.REFUND:
            refund = self._client.v1.refunds.create(
                {"payment_intent": reference, "metadata": _metadata(request)}, options
            )
            return _outcome(refund, "succeeded")
        raise ValueError(f"unsupported operation kind {request.kind!r}")

    def _authorize(self, request: ProviderOperationRequest) -> ProviderOperationOutcome:
        amount = _minor_units(request.amount)
        if amount is None:
            return ProviderOperationOutcome.declined("stripe_unsupported_amount")
        declined = request.amount.amount == DECLINE_AMOUNT
        intent = self._client.v1.payment_intents.create(
            {
                "amount": amount,
                "currency": request.amount.currency.lower(),
                "capture_method": "manual",
                "confirm": True,
                "payment_method": DECLINED_PAYMENT_METHOD if declined else APPROVED_PAYMENT_METHOD,
                # A confirmation without a browser: no method that needs a redirect.
                "automatic_payment_methods": {"enabled": True, "allow_redirects": "never"},
                "metadata": _metadata(request),
            },
            {"idempotency_key": request.idempotency_key},
        )
        return _outcome(intent, "requires_capture")


def _metadata(request: ProviderOperationRequest) -> dict[str, str]:
    """Stripe returns it in webhooks: this is how a webhook finds its operation."""
    return {"operation_id": str(request.operation_id), "payment_id": str(request.payment_id)}


def _minor_units(money: Money) -> int | None:
    minor = money.amount * Decimal(100)
    if money.currency not in SUPPORTED_CURRENCIES or minor != minor.to_integral_value():
        return None
    return int(minor)


def _outcome(result: _StripeObject, completed_status: str) -> ProviderOperationOutcome:
    if result.status == completed_status:
        return ProviderOperationOutcome.succeeded(result.id)
    if result.status in {"processing", "pending"}:
        return ProviderOperationOutcome.accepted()
    return ProviderOperationOutcome.declined(f"stripe_status_{result.status}")
