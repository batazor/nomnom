import unittest
from typing import override

from payment_fakes import OPERATOR, ORCHESTRATOR, PaymentsTestKit

from pkg.application import PermissionDeniedError
from slices.payment.applications.errors import PaymentStateConflictError
from slices.payment.applications.operation_flow import OperationStatus
from slices.payment.applications.ports import PaymentPermission, ProviderOperationOutcome
from slices.payment.applications.refund_payment import RefundPaymentHandler, RefundPaymentInput
from slices.payment.domains.payment import (
    Money,
    OperationKind,
    OrderId,
    PaymentOperationDeclined,
    PaymentRefunded,
    PaymentState,
)

KEY = "order-1:refund"


class RefundPaymentTest(unittest.TestCase):
    @override
    def setUp(self) -> None:
        self.kit = PaymentsTestKit()
        self.handler = RefundPaymentHandler(self.kit.executor())

    def test_ac_416_successful_refund_of_full_amount(self) -> None:
        payment_id = self.kit.given_payment(PaymentState.CAPTURED)

        result = self.handler.handle(RefundPaymentInput(OPERATOR, payment_id, KEY))

        self.assertEqual(result.status, OperationStatus.SUCCEEDED)
        self.assertEqual(self.kit.payment_state(payment_id), PaymentState.REFUNDED)
        self.assertEqual(self.kit.provider.requests[0].amount, Money.parse("25.00", "EUR"))
        self.assertEqual(
            self.kit.published(),
            [PaymentRefunded(payment_id, OrderId("order-1"), Money.parse("25.00", "EUR"))],
        )
        self.assertEqual(self.kit.permissions.checks[0][:2], (OPERATOR, PaymentPermission.REFUND))

    def test_ac_417_refund_before_capture_is_rejected(self) -> None:
        for state in (PaymentState.PENDING, PaymentState.AUTHORIZED, PaymentState.FAILED):
            with self.subTest(state=state):
                payment_id = self.kit.given_payment(state, payment_id=f"payment-{state.value}")

                with self.assertRaises(PaymentStateConflictError):
                    self.handler.handle(RefundPaymentInput(ORCHESTRATOR, payment_id, KEY))

        self.assertEqual(self.kit.provider.requests, [])

    def test_declined_refund_keeps_payment_captured(self) -> None:
        payment_id = self.kit.given_payment(PaymentState.CAPTURED)
        self.kit.provider.respond(
            OperationKind.REFUND, ProviderOperationOutcome.declined("refund_window_closed")
        )

        result = self.handler.handle(RefundPaymentInput(ORCHESTRATOR, payment_id, KEY))

        self.assertEqual(result.status, OperationStatus.DECLINED)
        self.assertEqual(self.kit.payment_state(payment_id), PaymentState.CAPTURED)
        self.assertEqual(
            [type(event) for event in self.kit.published()], [PaymentOperationDeclined]
        )

    def test_operator_without_permission_cannot_refund(self) -> None:
        payment_id = self.kit.given_payment(PaymentState.CAPTURED)
        self.kit.permissions.denied.add(PaymentPermission.REFUND)

        with self.assertRaises(PermissionDeniedError):
            self.handler.handle(RefundPaymentInput(OPERATOR, payment_id, KEY))

        self.assertEqual(self.kit.payment_state(payment_id), PaymentState.CAPTURED)
        self.assertEqual(self.kit.provider.requests, [])


if __name__ == "__main__":
    unittest.main()
