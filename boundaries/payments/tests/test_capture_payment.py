import unittest
from typing import override

import pytest
from payment_fakes import ORCHESTRATOR, PaymentsTestKit

from infrastructure.repository import PostgresDatabase
from slices.payment.applications.capture_payment import CapturePaymentHandler, CapturePaymentInput
from slices.payment.applications.errors import PaymentStateConflictError
from slices.payment.applications.operation_flow import OperationStatus
from slices.payment.applications.ports import ProviderOperationOutcome
from slices.payment.domains.payment import (
    Money,
    OperationKind,
    OrderId,
    PaymentCaptured,
    PaymentOperationDeclined,
    PaymentState,
)

KEY = "order-1:capture"


@pytest.mark.usefixtures("postgres")
class CapturePaymentTest(unittest.TestCase):
    database: PostgresDatabase

    @override
    def setUp(self) -> None:
        self.kit = PaymentsTestKit(self.database)
        self.handler = CapturePaymentHandler(self.kit.executor())

    def capture(self, payment_id: str) -> CapturePaymentInput:
        return CapturePaymentInput(ORCHESTRATOR, payment_id, KEY)

    def test_ac_412_successful_capture_of_full_amount(self) -> None:
        payment_id = self.kit.given_payment(PaymentState.AUTHORIZED)

        result = self.handler.handle(self.capture(payment_id))

        self.assertEqual(result.status, OperationStatus.SUCCEEDED)
        self.assertEqual(self.kit.payment_state(payment_id), PaymentState.CAPTURED)
        request = self.kit.provider.requests[0]
        self.assertEqual(request.amount, Money.parse("25.00", "EUR"))
        self.assertEqual(request.authorization_reference, "auth-ref")
        self.assertEqual(
            self.kit.published(),
            [PaymentCaptured(payment_id, OrderId("order-1"), Money.parse("25.00", "EUR"))],
        )

    def test_ac_413_declined_capture_keeps_authorization(self) -> None:
        payment_id = self.kit.given_payment(PaymentState.AUTHORIZED)
        self.kit.provider.respond(
            OperationKind.CAPTURE, ProviderOperationOutcome.declined("authorization_expired")
        )

        result = self.handler.handle(self.capture(payment_id))

        self.assertEqual(result.status, OperationStatus.DECLINED)
        self.assertEqual(result.payment_state, PaymentState.AUTHORIZED)
        self.assertEqual(self.kit.payment_state(payment_id), PaymentState.AUTHORIZED)
        self.assertEqual(
            self.kit.published(),
            [
                PaymentOperationDeclined(
                    result.operation_id, payment_id, OperationKind.CAPTURE, "authorization_expired"
                )
            ],
        )

    def test_ac_407_repeated_capture_has_one_financial_effect(self) -> None:
        payment_id = self.kit.given_payment(PaymentState.AUTHORIZED)
        first = self.handler.handle(self.capture(payment_id))

        second = self.handler.handle(self.capture(payment_id))

        self.assertTrue(second.replayed)
        self.assertEqual(second.operation_id, first.operation_id)
        self.assertEqual(second.payment_state, PaymentState.CAPTURED)
        self.assertEqual(len(self.kit.provider.requests), 1)
        self.assertEqual(len(self.kit.published()), 1)

    def test_ac_410_capture_before_authorization_is_rejected(self) -> None:
        payment_id = self.kit.given_payment(PaymentState.PENDING)

        with self.assertRaises(PaymentStateConflictError) as raised:
            self.handler.handle(self.capture(payment_id))

        self.assertEqual(raised.exception.state, PaymentState.PENDING)
        self.assertEqual(self.kit.provider.requests, [])
        self.assertEqual(self.kit.stored_operations(), [])

    def test_new_key_for_already_captured_payment_is_rejected(self) -> None:
        payment_id = self.kit.given_payment(PaymentState.CAPTURED)

        with self.assertRaises(PaymentStateConflictError):
            self.handler.handle(self.capture(payment_id))

        self.assertEqual(self.kit.provider.requests, [])


if __name__ == "__main__":
    unittest.main()
