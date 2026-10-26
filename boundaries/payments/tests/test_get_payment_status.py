import dataclasses
import unittest
from decimal import Decimal
from typing import override

import pytest
from payment_fakes import MARKET, OPERATOR, ORCHESTRATOR, PaymentsTestKit

from infrastructure.repository import Database
from pkg.application import PermissionDeniedError
from slices.payment.applications.errors import PaymentNotFoundError
from slices.payment.applications.get_payment_status import (
    GetPaymentStatusHandler,
    GetPaymentStatusInput,
)
from slices.payment.applications.ports import PaymentPermission
from slices.payment.domains.payment import OrderId, PaymentState


@pytest.mark.usefixtures("database")
class GetPaymentStatusTest(unittest.TestCase):
    database: Database

    @override
    def setUp(self) -> None:
        self.kit = PaymentsTestKit(self.database)
        self.handler = GetPaymentStatusHandler(self.kit.statuses, self.kit.permissions)

    def test_ac_428_returns_state_without_provider_data(self) -> None:
        payment_id = self.kit.given_payment(PaymentState.AUTHORIZED)
        before = self.kit.snapshot()

        result = self.handler.handle(GetPaymentStatusInput(ORCHESTRATOR, payment_id))

        self.assertEqual(result.payment_id, payment_id)
        self.assertEqual(result.order_id, OrderId("order-1"))
        self.assertEqual(result.market_id, MARKET)
        self.assertEqual((result.amount, result.currency), (Decimal("25.00"), "EUR"))
        self.assertEqual(result.state, PaymentState.AUTHORIZED)
        fields = {field.name for field in dataclasses.fields(result)}
        self.assertNotIn("provider_reference", fields)
        self.assertNotIn("auth-ref", repr(result))
        self.assertEqual(self.kit.snapshot(), before)

    def test_ac_429_unknown_payment_is_not_found(self) -> None:
        with self.assertRaises(PaymentNotFoundError):
            self.handler.handle(GetPaymentStatusInput(ORCHESTRATOR, "missing"))

    def test_denied_subject_learns_nothing_about_existence(self) -> None:
        self.kit.permissions.denied.add(PaymentPermission.VIEW)

        with self.assertRaises(PermissionDeniedError):
            self.handler.handle(GetPaymentStatusInput(OPERATOR, "missing"))


if __name__ == "__main__":
    unittest.main()
