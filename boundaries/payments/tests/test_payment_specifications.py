import unittest
from datetime import datetime, timedelta, timezone

from slices.payment.domains.payment.specifications import (
    PaymentCapturable,
    PaymentCaptureContext,
    PaymentPolicyReason,
    PaymentRefundable,
    PaymentRefundContext,
)
from slices.payment.domains.payment.vo.money import Money


class PaymentSpecificationsTest(unittest.TestCase):
    def test_refund_is_allowed_on_time_and_amount_boundaries(self) -> None:
        captured_at = self.instant()
        result = PaymentRefundable(
            PaymentRefundContext(
                now=captured_at + timedelta(days=30),
                captured_at=captured_at,
                refund_window=timedelta(days=30),
                captured_amount=self.money("10"),
                already_refunded=self.money("4"),
                requested_amount=self.money("6"),
            )
        )

        self.assertTrue(result.satisfied)
        self.assertEqual(result.reasons, ())

    def test_refund_collects_expiry_and_amount_reasons(self) -> None:
        captured_at = self.instant()
        result = PaymentRefundable(
            PaymentRefundContext(
                now=captured_at + timedelta(days=30, microseconds=1),
                captured_at=captured_at,
                refund_window=timedelta(days=30),
                captured_amount=self.money("10"),
                already_refunded=self.money("4"),
                requested_amount=self.money("7"),
            )
        )

        self.assertFalse(result.satisfied)
        self.assertEqual(
            result.reasons,
            (
                PaymentPolicyReason.REFUND_WINDOW_EXPIRED,
                PaymentPolicyReason.REFUND_AMOUNT_EXCEEDS_AVAILABLE,
            ),
        )

    def test_capture_is_allowed_on_time_and_amount_boundaries(self) -> None:
        authorized_at = self.instant()
        result = PaymentCapturable(
            PaymentCaptureContext(
                now=authorized_at + timedelta(minutes=15),
                authorized_at=authorized_at,
                authorization_window=timedelta(minutes=15),
                authorized_amount=self.money("10"),
                already_captured=self.money("4"),
                requested_amount=self.money("6"),
            )
        )

        self.assertTrue(result.satisfied)

    def test_capture_collects_expiry_and_amount_reasons(self) -> None:
        authorized_at = self.instant()
        result = PaymentCapturable(
            PaymentCaptureContext(
                now=authorized_at + timedelta(minutes=15, microseconds=1),
                authorized_at=authorized_at,
                authorization_window=timedelta(minutes=15),
                authorized_amount=self.money("10"),
                already_captured=self.money("4"),
                requested_amount=self.money("7"),
            )
        )

        self.assertEqual(
            result.reasons,
            (
                PaymentPolicyReason.AUTHORIZATION_EXPIRED,
                PaymentPolicyReason.CAPTURE_AMOUNT_EXCEEDS_AVAILABLE,
            ),
        )

    @staticmethod
    def instant() -> datetime:
        return datetime(2026, 10, 25, 10, 0, tzinfo=timezone.utc)

    @staticmethod
    def money(amount: str) -> Money:
        return Money.parse(amount, "EUR")


if __name__ == "__main__":
    unittest.main()
