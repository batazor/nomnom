import dataclasses
import unittest
from datetime import timedelta
from typing import override

import pytest
from payment_fakes import OPERATOR, ORCHESTRATOR, PaymentsTestKit

from infrastructure.postgres import PostgresDatabase
from pkg.application import PermissionDeniedError
from slices.payment.applications.authorize_payment import (
    AuthorizePaymentHandler,
    AuthorizePaymentInput,
)
from slices.payment.applications.capture_payment import CapturePaymentHandler, CapturePaymentInput
from slices.payment.applications.errors import PaymentNotFoundError
from slices.payment.applications.list_payment_operations import (
    ListPaymentOperationsHandler,
    ListPaymentOperationsInput,
    PaymentOperationView,
)
from slices.payment.applications.ports import PaymentPermission, ProviderOperationOutcome
from slices.payment.domains.payment import OperationKind, OperationState


@pytest.mark.usefixtures("postgres")
class ListPaymentOperationsTest(unittest.TestCase):
    database: PostgresDatabase

    @override
    def setUp(self) -> None:
        self.kit = PaymentsTestKit(self.database)
        self.payment_id = self.kit.given_payment()
        self.handler = ListPaymentOperationsHandler(
            self.kit.payments, self.kit.history, self.kit.permissions
        )

    def test_ac_430_returns_history_in_start_order_without_provider_data(self) -> None:
        executor = self.kit.executor()
        AuthorizePaymentHandler(executor).handle(
            AuthorizePaymentInput(ORCHESTRATOR, self.payment_id, "order-1:authorize")
        )
        self.kit.clock.advance(timedelta(minutes=5))
        self.kit.provider.respond(
            OperationKind.CAPTURE, ProviderOperationOutcome.declined("authorization_expired")
        )
        CapturePaymentHandler(executor).handle(
            CapturePaymentInput(ORCHESTRATOR, self.payment_id, "order-1:capture")
        )
        before = self.kit.snapshot()

        result = self.handler.handle(ListPaymentOperationsInput(OPERATOR, self.payment_id))

        self.assertEqual(
            [(op.kind, op.state, op.decline_reason) for op in result.operations],
            [
                (OperationKind.AUTHORIZE, OperationState.SUCCEEDED, None),
                (OperationKind.CAPTURE, OperationState.DECLINED, "authorization_expired"),
            ],
        )
        first, second = result.operations
        self.assertLess(first.requested_at, second.requested_at)
        self.assertEqual(second.completed_at, self.kit.clock.now())
        fields = {field.name for field in dataclasses.fields(PaymentOperationView)}
        self.assertNotIn("provider_reference", fields)
        self.assertEqual(self.kit.snapshot(), before)

    def test_ac_431_subject_without_permission_is_rejected(self) -> None:
        self.kit.permissions.denied.add(PaymentPermission.VIEW_OPERATIONS)

        with self.assertRaises(PermissionDeniedError):
            self.handler.handle(ListPaymentOperationsInput(OPERATOR, self.payment_id))

    def test_unknown_payment_is_not_found(self) -> None:
        with self.assertRaises(PaymentNotFoundError):
            self.handler.handle(ListPaymentOperationsInput(OPERATOR, "missing"))

    def test_payment_without_operations_has_empty_history(self) -> None:
        result = self.handler.handle(ListPaymentOperationsInput(OPERATOR, self.payment_id))

        self.assertEqual(result.operations, ())


if __name__ == "__main__":
    unittest.main()
