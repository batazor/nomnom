import json
import os
import unittest
from datetime import timedelta
from types import SimpleNamespace
from unittest.mock import MagicMock, patch

import stripe
from pydantic import ValidationError

from pkg.config import ProviderSettings
from slices.payment.applications.ports import (
    ProviderOperationRequest,
    ProviderOperationStatus,
    ProviderUnavailableError,
)
from slices.payment.di.payment import create_provider, webhook_format_of
from slices.payment.domains.payment import Money, OperationId, OperationKind, PaymentId
from slices.payment.infrastructure.provider import (
    APPROVED_PAYMENT_METHOD,
    DECLINE_AMOUNT,
    DECLINED_PAYMENT_METHOD,
    SANDBOX_WEBHOOK_FORMAT,
    STRIPE_WEBHOOK_FORMAT,
    InvalidWebhookError,
    StripePaymentProvider,
    parse_stripe_event,
)


def _request(
    kind: OperationKind, amount: str = "10.50", currency: str = "EUR", auth: str | None = "pi_1"
) -> ProviderOperationRequest:
    return ProviderOperationRequest(
        OperationId("op-1"), PaymentId("pay-1"), kind, "key-1", Money.parse(amount, currency), auth
    )


class StripeProviderTest(unittest.TestCase):
    def test_authorize_creates_confirmed_intent_with_manual_capture(self) -> None:
        client = MagicMock()
        client.v1.payment_intents.create.return_value = SimpleNamespace(
            id="pi_1", status="requires_capture"
        )

        outcome = StripePaymentProvider(client).execute(_request(OperationKind.AUTHORIZE))

        self.assertIs(outcome.status, ProviderOperationStatus.SUCCEEDED)
        self.assertEqual(outcome.provider_reference, "pi_1")
        client.v1.payment_intents.create.assert_called_once_with(
            {
                "amount": 1050,
                "currency": "eur",
                "capture_method": "manual",
                "confirm": True,
                "payment_method": APPROVED_PAYMENT_METHOD,
                "automatic_payment_methods": {"enabled": True, "allow_redirects": "never"},
                "metadata": {"operation_id": "op-1", "payment_id": "pay-1"},
            },
            {"idempotency_key": "key-1"},
        )

    def test_decline_amount_uses_declining_card(self) -> None:
        client = MagicMock()
        client.v1.payment_intents.create.side_effect = stripe.CardError(
            "declined", None, "card_declined"
        )

        outcome = StripePaymentProvider(client).execute(
            _request(OperationKind.AUTHORIZE, amount=str(DECLINE_AMOUNT))
        )

        self.assertIs(outcome.status, ProviderOperationStatus.DECLINED)
        self.assertEqual(outcome.decline_reason, "card_declined")
        params = client.v1.payment_intents.create.call_args.args[0]
        self.assertEqual(params["payment_method"], DECLINED_PAYMENT_METHOD)

    def test_unsupported_amount_is_declined_without_a_request(self) -> None:
        for amount, currency in (("10.00", "JPY"), ("10.00", "KWD"), ("10.005", "EUR")):
            with self.subTest(amount=amount, currency=currency):
                client = MagicMock()
                outcome = StripePaymentProvider(client).execute(
                    _request(OperationKind.AUTHORIZE, amount, currency)
                )
                self.assertEqual(outcome.decline_reason, "stripe_unsupported_amount")
                client.v1.payment_intents.create.assert_not_called()

    def test_capture_passes_operation_in_metadata(self) -> None:
        client = MagicMock()
        client.v1.payment_intents.capture.return_value = SimpleNamespace(
            id="pi_1", status="succeeded"
        )

        outcome = StripePaymentProvider(client).execute(_request(OperationKind.CAPTURE))

        self.assertEqual(outcome.provider_reference, "pi_1")
        client.v1.payment_intents.capture.assert_called_once_with(
            "pi_1",
            {"metadata": {"operation_id": "op-1", "payment_id": "pay-1"}},
            {"idempotency_key": "key-1"},
        )

    def test_cancel_authorization_cancels_intent(self) -> None:
        client = MagicMock()
        client.v1.payment_intents.cancel.return_value = SimpleNamespace(
            id="pi_1", status="canceled"
        )

        outcome = StripePaymentProvider(client).execute(
            _request(OperationKind.CANCEL_AUTHORIZATION)
        )

        self.assertIs(outcome.status, ProviderOperationStatus.SUCCEEDED)
        client.v1.payment_intents.cancel.assert_called_once_with(
            "pi_1", options={"idempotency_key": "key-1"}
        )

    def test_refund_status_maps_to_outcome(self) -> None:
        for status, expected in (
            ("succeeded", ProviderOperationStatus.SUCCEEDED),
            ("pending", ProviderOperationStatus.ACCEPTED),
            ("failed", ProviderOperationStatus.DECLINED),
        ):
            with self.subTest(status=status):
                client = MagicMock()
                client.v1.refunds.create.return_value = SimpleNamespace(id="re_1", status=status)
                outcome = StripePaymentProvider(client).execute(_request(OperationKind.REFUND))
                self.assertIs(outcome.status, expected)

    def test_operation_without_authorization_is_declined(self) -> None:
        outcome = StripePaymentProvider(MagicMock()).execute(
            _request(OperationKind.CAPTURE, auth=None)
        )
        self.assertEqual(outcome.decline_reason, "authorization_not_found")

    def test_invalid_request_is_declined(self) -> None:
        client = MagicMock()
        client.v1.payment_intents.create.side_effect = stripe.InvalidRequestError(
            "too small", "amount", "amount_too_small"
        )
        outcome = StripePaymentProvider(client).execute(_request(OperationKind.AUTHORIZE))
        self.assertEqual(outcome.decline_reason, "stripe_invalid_request")

    def test_technical_errors_are_retryable(self) -> None:
        for error in (
            stripe.APIConnectionError("no route"),
            stripe.RateLimitError("slow down"),
            stripe.AuthenticationError("bad key"),
            stripe.APIError("internal"),
        ):
            with self.subTest(error=type(error).__name__):
                client = MagicMock()
                client.v1.payment_intents.create.side_effect = error
                with self.assertRaises(ProviderUnavailableError):
                    StripePaymentProvider(client).execute(_request(OperationKind.AUTHORIZE))


class StripeSettingsTest(unittest.TestCase):
    def test_requires_test_key(self) -> None:
        for key in ("", "sk_live_x", "rk_test_x"):
            with (
                self.subTest(key=key),
                patch.dict(
                    os.environ,
                    {"PROVIDER__KIND": "stripe", "PROVIDER__STRIPE_API_KEY": key},
                    clear=True,
                ),
                self.assertRaises(ValidationError),
            ):
                ProviderSettings()

    def test_key_does_not_reach_repr(self) -> None:
        with patch.dict(
            os.environ,
            {"PROVIDER__KIND": "stripe", "PROVIDER__STRIPE_API_KEY": "sk_test_hidden"},
            clear=True,
        ):
            settings = ProviderSettings()
        self.assertNotIn("sk_test_hidden", repr(settings))
        self.assertEqual(settings.stripe_timeout, timedelta(seconds=10))

    def test_kind_selects_provider_and_webhook_format(self) -> None:
        with patch.dict(
            os.environ,
            {"PROVIDER__KIND": "stripe", "PROVIDER__STRIPE_API_KEY": "sk_test_x"},
            clear=True,
        ):
            settings = ProviderSettings()
        self.assertIsInstance(create_provider(settings), StripePaymentProvider)
        self.assertIs(webhook_format_of(settings), STRIPE_WEBHOOK_FORMAT)
        with patch.dict(os.environ, {"PROVIDER__KIND": "fake"}, clear=True):
            self.assertIs(webhook_format_of(ProviderSettings()), SANDBOX_WEBHOOK_FORMAT)


class StripeEventTest(unittest.TestCase):
    def test_authorized_intent_succeeds_its_operation(self) -> None:
        command = parse_stripe_event(
            json.dumps(
                {
                    "id": "evt_1",
                    "type": "payment_intent.amount_capturable_updated",
                    "data": {"object": {"id": "pi_1", "metadata": {"operation_id": "op-1"}}},
                }
            ).encode()
        )
        assert command is not None
        self.assertEqual(command.provider_message_id, "evt_1")
        self.assertEqual(command.operation_id, "op-1")
        self.assertEqual(command.outcome.provider_reference, "pi_1")

    def test_failed_intent_declines_with_error_code(self) -> None:
        command = parse_stripe_event(
            json.dumps(
                {
                    "id": "evt_1",
                    "type": "payment_intent.payment_failed",
                    "data": {
                        "object": {
                            "id": "pi_1",
                            "metadata": {"operation_id": "op-1"},
                            "last_payment_error": {"code": "card_declined"},
                        }
                    },
                }
            ).encode()
        )
        assert command is not None
        self.assertIs(command.outcome.status, ProviderOperationStatus.DECLINED)
        self.assertEqual(command.outcome.decline_reason, "card_declined")

    def test_refund_events_map_final_statuses(self) -> None:
        for event_type, status, expected in (
            ("refund.updated", "succeeded", ProviderOperationStatus.SUCCEEDED),
            ("refund.failed", "failed", ProviderOperationStatus.DECLINED),
        ):
            with self.subTest(event_type=event_type):
                command = parse_stripe_event(
                    json.dumps(
                        {
                            "id": "evt_1",
                            "type": event_type,
                            "data": {
                                "object": {
                                    "id": "re_1",
                                    "status": status,
                                    "failure_reason": "expired_or_canceled_card",
                                    "metadata": {"operation_id": "op-1"},
                                }
                            },
                        }
                    ).encode()
                )
                assert command is not None
                self.assertIs(command.outcome.status, expected)

    def test_events_without_an_operation_are_ignored(self) -> None:
        for event in (
            {"type": "charge.succeeded", "object": {"id": "ch_1"}},
            {"type": "payment_intent.canceled", "object": {"id": "pi_1"}},
            {"type": "payment_intent.succeeded", "object": {"id": "pi_1", "metadata": {}}},
            {
                "type": "refund.updated",
                "object": {
                    "id": "re_1",
                    "status": "pending",
                    "metadata": {"operation_id": "op-1"},
                },
            },
        ):
            with self.subTest(event=event["type"]):
                body = json.dumps(
                    {"id": "evt_1", "type": event["type"], "data": {"object": event["object"]}}
                ).encode()
                self.assertIsNone(parse_stripe_event(body))

    def test_malformed_event_is_rejected(self) -> None:
        for body in (
            b"not json",
            b"{}",
            b'{"id":"e","type":"t"}',
            b'{"id":"e","type":"t","data":{"object":{}}}',
        ):
            with self.subTest(body=body), self.assertRaises(InvalidWebhookError):
                parse_stripe_event(body)


if __name__ == "__main__":
    unittest.main()
