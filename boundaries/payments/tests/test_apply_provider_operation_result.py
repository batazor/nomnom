import unittest

from payment_fakes import ORCHESTRATOR, PaymentsTestKit

from slices.payment.applications.apply_provider_operation_result import (
    ApplyProviderOperationResultHandler,
    ApplyProviderOperationResultInput,
)
from slices.payment.applications.authorize_payment import (
    AuthorizePaymentHandler,
    AuthorizePaymentInput,
)
from slices.payment.applications.errors import PaymentOperationNotFound, ProviderResultConflict
from slices.payment.applications.operation_flow import OperationStatus, ProviderResultStatus
from slices.payment.applications.ports import ProviderOperationOutcome
from slices.payment.domains.payment import (
    OperationKind,
    OperationState,
    PaymentFailed,
    PaymentOperationDeclined,
    PaymentState,
)


class ApplyProviderOperationResultTest(unittest.TestCase):
    def setUp(self) -> None:
        self.kit = PaymentsTestKit()
        self.payment_id = self.kit.given_payment()
        self.handler = ApplyProviderOperationResultHandler(self.kit.uow, self.kit.applier())

    def accepted_authorization(self) -> str:
        self.kit.provider.respond(OperationKind.AUTHORIZE, ProviderOperationOutcome.accepted())
        result = AuthorizePaymentHandler(self.kit.executor()).handle(
            AuthorizePaymentInput(ORCHESTRATOR, self.payment_id, "order-1:authorize")
        )
        self.assertEqual(result.status, OperationStatus.PENDING)
        return result.operation_id

    def test_ac_423_result_of_pending_operation_is_applied(self) -> None:
        operation_id = self.accepted_authorization()

        result = self.handler.handle(
            ApplyProviderOperationResultInput(
                operation_id, ProviderOperationOutcome.declined("card_declined")
            )
        )

        self.assertEqual(result.status, ProviderResultStatus.APPLIED)
        self.assertEqual(result.operation_status, OperationStatus.DECLINED)
        self.assertEqual(self.kit.payment_state(self.payment_id), PaymentState.FAILED)
        self.assertEqual(
            [type(e) for e in self.kit.published()], [PaymentOperationDeclined, PaymentFailed]
        )

    def test_ac_424_repeated_result_is_a_no_op(self) -> None:
        operation_id = self.accepted_authorization()
        command = ApplyProviderOperationResultInput(
            operation_id, ProviderOperationOutcome.succeeded("auth-ref")
        )
        self.handler.handle(command)

        repeated = self.handler.handle(command)

        self.assertEqual(repeated.status, ProviderResultStatus.ALREADY_APPLIED)
        self.assertEqual(repeated.payment_state, PaymentState.AUTHORIZED)
        self.assertEqual(len(self.kit.published()), 1)

    def test_ac_425_contradicting_result_is_rejected(self) -> None:
        operation_id = self.kit.given_operation(
            self.payment_id,
            OperationKind.AUTHORIZE,
            "order-1:authorize",
            state=OperationState.DECLINED,
            decline_reason="card_declined",
        )

        with self.assertRaises(ProviderResultConflict):
            self.handler.handle(
                ApplyProviderOperationResultInput(
                    operation_id, ProviderOperationOutcome.succeeded("auth-ref")
                )
            )

        self.assertEqual(self.kit.operation(operation_id).state, OperationState.DECLINED)
        self.assertEqual(self.kit.payment_state(self.payment_id), PaymentState.PENDING)
        self.assertEqual(self.kit.published(), [])

    def test_unknown_operation_is_not_found(self) -> None:
        with self.assertRaises(PaymentOperationNotFound):
            self.handler.handle(
                ApplyProviderOperationResultInput("missing", ProviderOperationOutcome.succeeded())
            )

    def test_retry_after_async_result_returns_recorded_result(self) -> None:
        operation_id = self.accepted_authorization()
        self.handler.handle(
            ApplyProviderOperationResultInput(
                operation_id, ProviderOperationOutcome.succeeded("auth-ref")
            )
        )

        retry = AuthorizePaymentHandler(self.kit.executor()).handle(
            AuthorizePaymentInput(ORCHESTRATOR, self.payment_id, "order-1:authorize")
        )

        self.assertTrue(retry.replayed)
        self.assertEqual(retry.status, OperationStatus.SUCCEEDED)
        self.assertEqual(len(self.kit.provider.requests), 1)
        self.assertEqual(len(self.kit.published()), 1)


if __name__ == "__main__":
    unittest.main()
