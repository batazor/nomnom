import json
import os
import unittest
from datetime import UTC, datetime, timedelta
from unittest.mock import patch

from pydantic import ValidationError

from pkg.config import ProviderSettings, load_settings
from slices.payment.applications.ports import (
    ProviderOperationRequest,
    ProviderOperationStatus,
)
from slices.payment.di.payment import ProviderConfigurationError, create_provider
from slices.payment.domains.payment import Money, OperationId, OperationKind, PaymentId
from slices.payment.infrastructure.provider import (
    DECLINE_AMOUNT,
    FakePaymentProvider,
    InvalidWebhookError,
    WebhookVerifier,
    parse_callback,
    sign_payload,
)

NOW = datetime(2026, 10, 25, 12, 0, tzinfo=UTC)
SECRET = "whsec_test"


def _request(kind: OperationKind, key: str, amount: str = "10.00", auth: str | None = None):
    return ProviderOperationRequest(
        OperationId(f"op-{key}"), PaymentId("pay-1"), kind, key, Money.parse(amount, "EUR"), auth
    )


class FakeProviderTest(unittest.TestCase):
    def test_authorize_succeeds_with_reference(self) -> None:
        outcome = FakePaymentProvider().execute(_request(OperationKind.AUTHORIZE, "k1"))
        self.assertIs(outcome.status, ProviderOperationStatus.SUCCEEDED)
        self.assertEqual(outcome.provider_reference, "fake_auth_op-k1")

    def test_repeat_with_same_key_returns_original_result(self) -> None:
        provider = FakePaymentProvider()
        first = provider.execute(_request(OperationKind.AUTHORIZE, "k1"))
        again = provider.execute(
            _request(OperationKind.AUTHORIZE, "k1", amount=str(DECLINE_AMOUNT))
        )
        self.assertEqual(first, again)

    def test_decline_amount_is_declined(self) -> None:
        outcome = FakePaymentProvider().execute(
            _request(OperationKind.AUTHORIZE, "k1", amount=str(DECLINE_AMOUNT))
        )
        self.assertIs(outcome.status, ProviderOperationStatus.DECLINED)

    def test_capture_requires_authorization_reference(self) -> None:
        provider = FakePaymentProvider()
        self.assertIs(
            provider.execute(_request(OperationKind.CAPTURE, "c1")).status,
            ProviderOperationStatus.DECLINED,
        )
        self.assertIs(
            provider.execute(_request(OperationKind.CAPTURE, "c2", auth="fake_auth_x")).status,
            ProviderOperationStatus.SUCCEEDED,
        )

    def test_create_provider_by_kind(self) -> None:
        with patch.dict(os.environ, {"PROVIDER__KIND": "fake"}, clear=True):
            self.assertIsInstance(create_provider(ProviderSettings()), FakePaymentProvider)
        with (
            patch.dict(os.environ, {}, clear=True),
            self.assertRaises(ProviderConfigurationError),
        ):
            create_provider(ProviderSettings())

    def test_rejects_unknown_kind(self) -> None:
        with (
            patch.dict(
                os.environ,
                {
                    "PROVIDER__KIND": "unknown",
                    "EVENT_STORE__SNAPSHOT_EVERY": "5",
                    "EVENT_STORE__SUBJECT_KEK": "k" * 32,
                },
                clear=True,
            ),
            self.assertRaises(ValidationError),
        ):
            load_settings()


class WebhookTest(unittest.TestCase):
    body = json.dumps(
        {"id": "msg-1", "operation_id": "op-1", "status": "succeeded", "provider_reference": "r1"}
    ).encode()

    def _verifier(self, secret: str = SECRET) -> WebhookVerifier:
        return WebhookVerifier(secret, timedelta(minutes=5))

    def test_valid_signature_is_accepted(self) -> None:
        header = sign_payload(SECRET, int(NOW.timestamp()), self.body)
        self._verifier().verify(header, self.body, NOW)

    def test_any_matching_v1_signature_is_accepted_for_secret_rotation(self) -> None:
        timestamp = int(NOW.timestamp())
        good = sign_payload(SECRET, timestamp, self.body).split("v1=")[1]
        self._verifier().verify(f"t={timestamp},v1=deadbeef,v1={good}", self.body, NOW)

    def test_tampered_body_is_rejected(self) -> None:
        header = sign_payload(SECRET, int(NOW.timestamp()), self.body)
        with self.assertRaises(InvalidWebhookError):
            self._verifier().verify(header, self.body + b" ", NOW)

    def test_empty_secret_is_a_config_error(self) -> None:
        with self.assertRaises(ValueError):
            self._verifier("")

    def test_other_secret_is_rejected(self) -> None:
        header = sign_payload("other", int(NOW.timestamp()), self.body)
        with self.assertRaises(InvalidWebhookError):
            self._verifier().verify(header, self.body, NOW)

    def test_stale_timestamp_is_rejected(self) -> None:
        header = sign_payload(SECRET, int((NOW - timedelta(minutes=6)).timestamp()), self.body)
        with self.assertRaises(InvalidWebhookError):
            self._verifier().verify(header, self.body, NOW)

    def test_missing_or_malformed_header_is_rejected(self) -> None:
        for header in (None, "", "v1=abc", "t=abc,v1=abc", f"t={int(NOW.timestamp())}"):
            with self.subTest(header=header), self.assertRaises(InvalidWebhookError):
                self._verifier().verify(header, self.body, NOW)

    def test_parse_maps_succeeded_and_declined(self) -> None:
        succeeded = parse_callback(self.body)
        self.assertEqual(succeeded.provider_message_id, "msg-1")
        self.assertEqual(succeeded.operation_id, "op-1")
        self.assertEqual(succeeded.outcome.provider_reference, "r1")
        declined = parse_callback(
            json.dumps(
                {"id": "m2", "operation_id": "op-2", "status": "declined", "decline_reason": "x"}
            ).encode()
        )
        self.assertIs(declined.outcome.status, ProviderOperationStatus.DECLINED)

    def test_parse_rejects_malformed_payload(self) -> None:
        for body in (
            b"not json",
            b"[]",
            b'{"id": "m"}',
            b'{"id":"m","operation_id":"o","status":"accepted"}',
        ):
            with self.subTest(body=body), self.assertRaises(InvalidWebhookError):
                parse_callback(body)


if __name__ == "__main__":
    unittest.main()
