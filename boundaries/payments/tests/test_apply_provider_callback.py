import unittest

from payment_fakes import PaymentsTestKit

from slices.payment.applications.apply_provider_callback import (
    ApplyProviderCallbackHandler,
    ApplyProviderCallbackInput,
)
from slices.payment.applications.errors import (
    InvalidPaymentRequestError,
    PaymentOperationNotFoundError,
    ProviderResultConflictError,
)
from slices.payment.applications.operation_flow import OperationStatus, ProviderResultStatus
from slices.payment.applications.ports import ProviderOperationOutcome
from slices.payment.domains.payment import (
    OperationKind,
    OperationState,
    PaymentCaptured,
    PaymentState,
)

CAPTURED = ProviderOperationOutcome.succeeded("capture-ref")


class ApplyProviderCallbackTest(unittest.TestCase):
    def setUp(self) -> None:
        self.kit = PaymentsTestKit()
        self.payment_id = self.kit.given_payment(PaymentState.AUTHORIZED)
        self.operation_id = self.kit.given_operation(
            self.payment_id, OperationKind.CAPTURE, "order-1:capture"
        )
        self.handler = ApplyProviderCallbackHandler(
            self.kit.uow, self.kit.inbox, self.kit.applier()
        )

    def callback(
        self,
        message_id: str = "message-1",
        operation_id: str = "",
        outcome: ProviderOperationOutcome = CAPTURED,
    ) -> ApplyProviderCallbackInput:
        return ApplyProviderCallbackInput(message_id, operation_id or self.operation_id, outcome)

    def test_ac_420_callback_applies_result_once(self) -> None:
        result = self.handler.handle(self.callback())

        self.assertEqual(result.status, ProviderResultStatus.APPLIED)
        self.assertEqual(result.operation_status, OperationStatus.SUCCEEDED)
        self.assertEqual(result.payment_state, PaymentState.CAPTURED)
        self.assertEqual(self.kit.operation(self.operation_id).state, OperationState.SUCCEEDED)
        self.assertEqual([type(e) for e in self.kit.published()], [PaymentCaptured])
        self.assertEqual(self.kit.provider.requests, [])

    def test_ac_421_repeated_message_is_a_no_op(self) -> None:
        self.handler.handle(self.callback())
        versions = self.kit.store.payments[self.payment_id].version

        duplicate = self.handler.handle(self.callback())

        self.assertEqual(duplicate.status, ProviderResultStatus.DUPLICATE_MESSAGE)
        self.assertEqual(self.kit.store.payments[self.payment_id].version, versions)
        self.assertEqual(len(self.kit.published()), 1)

    def test_new_message_with_same_result_is_already_applied(self) -> None:
        self.handler.handle(self.callback())

        repeated = self.handler.handle(self.callback(message_id="message-2"))

        self.assertEqual(repeated.status, ProviderResultStatus.ALREADY_APPLIED)
        self.assertEqual(len(self.kit.published()), 1)

    def test_ac_422_unknown_operation_is_rejected_and_message_not_registered(self) -> None:
        with self.assertRaises(PaymentOperationNotFoundError):
            self.handler.handle(self.callback(operation_id="unknown"))

        self.assertEqual(self.kit.store.provider_messages, set())
        self.assertEqual(self.kit.published(), [])

    def test_conflicting_result_rolls_back_message_registration(self) -> None:
        self.handler.handle(self.callback())

        with self.assertRaises(ProviderResultConflictError):
            self.handler.handle(
                self.callback(
                    message_id="message-2", outcome=ProviderOperationOutcome.declined("late")
                )
            )

        self.assertEqual(self.kit.store.provider_messages, {"message-1"})
        self.assertEqual(self.kit.payment_state(self.payment_id), PaymentState.CAPTURED)

    def test_non_final_or_malformed_callback_is_invalid(self) -> None:
        cases = [
            self.callback(outcome=ProviderOperationOutcome.accepted()),
            self.callback(outcome=ProviderOperationOutcome.declined(" ")),
            self.callback(message_id=" "),
        ]
        for command in cases:
            with self.subTest(command=command), self.assertRaises(InvalidPaymentRequestError):
                self.handler.handle(command)
        self.assertEqual(self.kit.store.provider_messages, set())


if __name__ == "__main__":
    unittest.main()
