import unittest

from payment_fakes import ORCHESTRATOR, PaymentsTestKit

from slices.payment.applications.cancel_authorization import (
    CancelAuthorizationHandler,
    CancelAuthorizationInput,
)
from slices.payment.applications.errors import PaymentStateConflict
from slices.payment.applications.operation_flow import OperationStatus
from slices.payment.applications.ports import ProviderOperationOutcome, ProviderUnavailable
from slices.payment.domains.payment import (
    Money,
    OperationKind,
    OrderId,
    PaymentAuthorizationCancelled,
    PaymentOperationDeclined,
    PaymentState,
)

KEY = "order-1:cancel-authorization"


class CancelAuthorizationTest(unittest.TestCase):
    def setUp(self) -> None:
        self.kit = PaymentsTestKit()
        self.handler = CancelAuthorizationHandler(self.kit.executor())

    def cancel(self, payment_id: str) -> CancelAuthorizationInput:
        return CancelAuthorizationInput(ORCHESTRATOR, payment_id, KEY)

    def test_ac_414_successful_cancellation_releases_authorization(self) -> None:
        payment_id = self.kit.given_payment(PaymentState.AUTHORIZED)

        result = self.handler.handle(self.cancel(payment_id))

        self.assertEqual(result.status, OperationStatus.SUCCEEDED)
        self.assertEqual(result.payment_state, PaymentState.PENDING)
        self.assertIsNone(self.kit.store.payments[payment_id].provider_reference)
        self.assertEqual(self.kit.provider.requests[0].authorization_reference, "auth-ref")
        self.assertEqual(
            self.kit.published(),
            [
                PaymentAuthorizationCancelled(
                    payment_id, OrderId("order-1"), Money.parse("25.00", "EUR")
                )
            ],
        )

    def test_ac_415_declined_cancellation_keeps_authorization(self) -> None:
        payment_id = self.kit.given_payment(PaymentState.AUTHORIZED)
        self.kit.provider.respond(
            OperationKind.CANCEL_AUTHORIZATION,
            ProviderOperationOutcome.declined("already_captured"),
        )

        result = self.handler.handle(self.cancel(payment_id))

        self.assertEqual(result.status, OperationStatus.DECLINED)
        self.assertEqual(self.kit.payment_state(payment_id), PaymentState.AUTHORIZED)
        self.assertEqual(
            [type(event) for event in self.kit.published()], [PaymentOperationDeclined]
        )

    def test_repeated_compensation_has_one_effect(self) -> None:
        payment_id = self.kit.given_payment(PaymentState.AUTHORIZED)
        self.kit.provider.lost_responses = 1
        with self.assertRaises(ProviderUnavailable):
            self.handler.handle(self.cancel(payment_id))

        result = self.handler.handle(self.cancel(payment_id))
        replay = self.handler.handle(self.cancel(payment_id))

        self.assertEqual(result.status, OperationStatus.SUCCEEDED)
        self.assertTrue(replay.replayed)
        self.assertEqual(self.kit.provider.effect_count(OperationKind.CANCEL_AUTHORIZATION), 1)
        self.assertEqual(len(self.kit.published()), 1)

    def test_cancellation_of_captured_payment_is_rejected(self) -> None:
        payment_id = self.kit.given_payment(PaymentState.CAPTURED)

        with self.assertRaises(PaymentStateConflict):
            self.handler.handle(self.cancel(payment_id))

        self.assertEqual(self.kit.provider.requests, [])


if __name__ == "__main__":
    unittest.main()
