import dataclasses
import unittest
from decimal import Decimal

from payment_fakes import OPERATOR, ORCHESTRATOR, PaymentsTestKit

from pkg.application import PermissionDeniedError
from slices.payment.applications.errors import PaymentNotFoundError
from slices.payment.applications.get_payment_status import (
    GetPaymentStatusHandler,
    GetPaymentStatusInput,
)
from slices.payment.applications.ports import PaymentPermission
from slices.payment.domains.payment import OrderId, PaymentState


class GetPaymentStatusTest(unittest.TestCase):
    def setUp(self) -> None:
        self.kit = PaymentsTestKit()
        self.handler = GetPaymentStatusHandler(self.kit.payments, self.kit.permissions)

    def test_ac_428_returns_state_without_provider_data(self) -> None:
        payment_id = self.kit.given_payment(PaymentState.AUTHORIZED)
        before = self.kit.store.snapshot()

        result = self.handler.handle(GetPaymentStatusInput(ORCHESTRATOR, payment_id))

        self.assertEqual(result.payment_id, payment_id)
        self.assertEqual(result.order_id, OrderId("order-1"))
        self.assertEqual((result.amount, result.currency), (Decimal("25.00"), "EUR"))
        self.assertEqual(result.state, PaymentState.AUTHORIZED)
        fields = {field.name for field in dataclasses.fields(result)}
        self.assertNotIn("provider_reference", fields)
        self.assertNotIn("auth-ref", repr(result))
        self.assertEqual(self.kit.store, before)

    def test_ac_429_unknown_payment_is_not_found(self) -> None:
        with self.assertRaises(PaymentNotFoundError):
            self.handler.handle(GetPaymentStatusInput(ORCHESTRATOR, "missing"))

    def test_denied_subject_learns_nothing_about_existence(self) -> None:
        self.kit.permissions.denied.add(PaymentPermission.VIEW)

        with self.assertRaises(PermissionDeniedError):
            self.handler.handle(GetPaymentStatusInput(OPERATOR, "missing"))


if __name__ == "__main__":
    unittest.main()
