import unittest
from typing import override

import pytest
from payment_fakes import MARKET, ORCHESTRATOR, PaymentsTestKit

from infrastructure.repository import Database
from pkg.authz import AuthorizationUnavailableError, PermissionDeniedError, Subject, SubjectKind
from pkg.outbox import AggregateRef
from slices.payment.applications.authorize import (
    AuthorizePaymentHandler,
    AuthorizePaymentInput,
)
from slices.payment.applications.capture import CapturePaymentHandler, CapturePaymentInput
from slices.payment.applications.errors import (
    IdempotencyKeyConflictError,
    InvalidPaymentRequestError,
    OperationInProgressError,
    PaymentNotFoundError,
)
from slices.payment.applications.operation_flow import OperationStatus
from slices.payment.applications.ports import (
    PaymentPermission,
    ProviderOperationOutcome,
    ProviderUnavailableError,
)
from slices.payment.domains.payment import (
    Money,
    OperationId,
    OperationKind,
    OperationRequester,
    OperationState,
    OrderId,
    PaymentAuthorized,
    PaymentFailed,
    PaymentId,
    PaymentOperationDeclined,
    PaymentState,
    RequesterKind,
    SealedSubject,
)

KEY = "order-1:authorize"


@pytest.mark.usefixtures("database")
class AuthorizePaymentTest(unittest.TestCase):
    database: Database

    @override
    def setUp(self) -> None:
        self.kit = PaymentsTestKit(self.database)
        self.payment_id = self.kit.given_payment()
        self.handler = AuthorizePaymentHandler(self.kit.executor())

    def authorize(self, key: str = KEY, payment_id: str = "") -> AuthorizePaymentInput:
        return AuthorizePaymentInput(ORCHESTRATOR, payment_id or self.payment_id, key)

    def test_ac_404_successful_authorization(self) -> None:
        result = self.handler.handle(self.authorize())

        self.assertEqual(result.status, OperationStatus.SUCCEEDED)
        self.assertEqual(result.payment_state, PaymentState.AUTHORIZED)
        self.assertFalse(result.replayed)
        self.assertEqual(self.kit.payment_state(self.payment_id), PaymentState.AUTHORIZED)
        self.assertEqual(self.kit.operation(result.operation_id).state, OperationState.SUCCEEDED)
        request = self.kit.provider.requests[0]
        self.assertEqual(request.idempotency_key, KEY)
        self.assertEqual(request.amount, Money.parse("25.00", "EUR"))
        self.assertEqual(request.operation_id, result.operation_id)
        self.assertEqual(
            self.kit.outbox_aggregates()[0], AggregateRef("Payment", self.payment_id, 4)
        )
        self.assertEqual(
            self.kit.published(),
            [
                PaymentAuthorized(
                    self.payment_id,
                    OrderId("order-1"),
                    MARKET,
                    Money.parse("25.00", "EUR"),
                    self.kit.payment(self.payment_id).provider_reference or "",
                )
            ],
        )

    def test_operation_records_who_requested_it(self) -> None:
        result = self.handler.handle(self.authorize())

        self.assertEqual(
            self.kit.requester_of(result.payment_id),
            OperationRequester(RequesterKind.SERVICE, ORCHESTRATOR.id),
        )

    def test_operation_records_the_user_and_the_delegate(self) -> None:
        user = Subject(SubjectKind.USER, "operator-1", delegate="bff")

        result = self.handler.handle(
            AuthorizePaymentInput(subject=user, payment_id=self.payment_id, idempotency_key=KEY)
        )

        requester = self.kit.requester_of(result.payment_id)
        assert requester is not None
        assert isinstance(requester.id, SealedSubject)
        self.assertEqual((requester.kind, requester.delegate), (RequesterKind.USER, "bff"))
        self.assertEqual(self.kit.subjects.reveal(requester.id), "operator-1")

    def test_ac_405_declined_authorization_fails_payment(self) -> None:
        self.kit.provider.respond(
            OperationKind.AUTHORIZE, ProviderOperationOutcome.declined("card_declined")
        )

        result = self.handler.handle(self.authorize())

        self.assertEqual(result.status, OperationStatus.DECLINED)
        self.assertEqual(result.decline_reason, "card_declined")
        self.assertEqual(result.payment_state, PaymentState.FAILED)
        self.assertEqual(self.kit.operation(result.operation_id).decline_reason, "card_declined")
        self.assertEqual(
            self.kit.published(),
            [
                PaymentOperationDeclined(
                    result.operation_id,
                    self.payment_id,
                    OperationKind.AUTHORIZE,
                    "card_declined",
                    self.kit.clock.now(),
                ),
                PaymentFailed(self.payment_id, OrderId("order-1"), MARKET, "card_declined"),
            ],
        )

    def test_ac_406_missing_idempotency_key_is_rejected(self) -> None:
        with self.assertRaises(InvalidPaymentRequestError):
            self.handler.handle(self.authorize(key="  "))

        self.assertEqual(self.kit.provider.requests, [])
        self.assertEqual(self.kit.permissions.checks, [])

    def test_ac_407_repeat_after_recorded_result_returns_it(self) -> None:
        first = self.handler.handle(self.authorize())

        second = self.handler.handle(self.authorize())

        self.assertTrue(second.replayed)
        self.assertEqual(second.operation_id, first.operation_id)
        self.assertEqual(second.status, OperationStatus.SUCCEEDED)
        self.assertEqual(len(self.kit.provider.requests), 1)
        self.assertEqual(len(self.kit.published()), 1)
        self.assertEqual(self.kit.payment(self.payment_id).version, 4)

    def test_ac_408_repeat_after_unknown_result_completes_same_operation(self) -> None:
        self.kit.provider.lost_responses = 1
        with self.assertRaises(ProviderUnavailableError):
            self.handler.handle(self.authorize())

        result = self.handler.handle(self.authorize())

        self.assertEqual(result.status, OperationStatus.SUCCEEDED)
        self.assertEqual(len(self.kit.stored_operations()), 1)
        self.assertEqual([r.idempotency_key for r in self.kit.provider.requests], [KEY, KEY])
        self.assertEqual(self.kit.provider.effect_count(OperationKind.AUTHORIZE), 1)
        self.assertEqual(len(self.kit.published()), 1)

    def test_ac_409_key_of_another_payment_or_kind_is_rejected(self) -> None:
        self.handler.handle(self.authorize())
        other_payment = self.kit.given_payment(payment_id="payment-other", order_id="order-2")
        capture = CapturePaymentHandler(self.kit.executor())

        with self.assertRaises(IdempotencyKeyConflictError):
            self.handler.handle(self.authorize(payment_id=other_payment))
        with self.assertRaises(IdempotencyKeyConflictError):
            capture.handle(CapturePaymentInput(ORCHESTRATOR, self.payment_id, KEY))

        self.assertEqual(len(self.kit.provider.requests), 1)
        self.assertEqual(self.kit.payment_state(other_payment), PaymentState.PENDING)

    def test_ac_411_another_pending_operation_blocks_new_one(self) -> None:
        self.kit.given_operation(self.payment_id, OperationKind.AUTHORIZE, "other-key")

        with self.assertRaises(OperationInProgressError):
            self.handler.handle(self.authorize())

        self.assertEqual(self.kit.provider.requests, [])

    def test_ac_418_unavailable_provider_keeps_state_and_pending_operation(self) -> None:
        self.kit.provider.unavailable_times = 1

        with self.assertRaises(ProviderUnavailableError):
            self.handler.handle(self.authorize())

        self.assertEqual(self.kit.payment_state(self.payment_id), PaymentState.PENDING)
        (operation,) = self.kit.stored_operations()
        self.assertEqual(operation.state, OperationState.PENDING)
        self.assertEqual(self.kit.published(), [])

    def test_ac_419_asynchronously_accepted_operation_is_pending(self) -> None:
        self.kit.provider.respond(OperationKind.AUTHORIZE, ProviderOperationOutcome.accepted())

        result = self.handler.handle(self.authorize())

        self.assertEqual(result.status, OperationStatus.PENDING)
        self.assertEqual(result.payment_state, PaymentState.PENDING)
        self.assertEqual(self.kit.operation(result.operation_id).state, OperationState.PENDING)
        self.assertEqual(self.kit.published(), [])

    def test_ac_426_denied_permission_does_not_reach_provider(self) -> None:
        self.kit.permissions.denied.add(PaymentPermission.AUTHORIZE)

        with self.assertRaises(PermissionDeniedError):
            self.handler.handle(self.authorize())

        self.assertEqual(self.kit.provider.requests, [])
        self.assertEqual(self.kit.stored_operations(), [])
        self.assertEqual(self.kit.payment_state(self.payment_id), PaymentState.PENDING)

    def test_ac_427_unavailable_authorization_fails_closed(self) -> None:
        self.kit.permissions.unavailable = True

        with self.assertRaises(AuthorizationUnavailableError):
            self.handler.handle(self.authorize())

        self.assertEqual(self.kit.provider.requests, [])
        self.assertEqual(self.kit.stored_operations(), [])

    def test_ac_432_outbox_failure_rolls_back_and_retry_completes(self) -> None:
        self.kit.outbox.fail_next = True

        with self.assertRaises(RuntimeError):
            self.handler.handle(self.authorize())

        self.assertEqual(self.kit.payment_state(self.payment_id), PaymentState.PENDING)
        (operation,) = self.kit.stored_operations()
        self.assertEqual(operation.state, OperationState.PENDING)
        self.assertEqual(self.kit.published(), [])

        result = self.handler.handle(self.authorize())

        self.assertEqual(result.status, OperationStatus.SUCCEEDED)
        self.assertEqual(self.kit.provider.effect_count(OperationKind.AUTHORIZE), 1)
        self.assertEqual(len(self.kit.published()), 1)

    def test_unknown_payment_is_not_found(self) -> None:
        with self.assertRaises(PaymentNotFoundError):
            self.handler.handle(self.authorize(payment_id="missing"))

        self.assertEqual(self.kit.provider.requests, [])

    def test_concurrent_request_with_same_key_reuses_its_operation(self) -> None:
        self.kit.given_operation(self.payment_id, OperationKind.AUTHORIZE, KEY, operation_id="op-x")
        self.kit.payments.stale_events = 1

        result = self.handler.handle(self.authorize())

        self.assertEqual(result.operation_id, OperationId("op-x"))
        self.assertEqual(result.status, OperationStatus.SUCCEEDED)
        self.assertEqual(len(self.kit.stored_operations()), 1)
        self.assertEqual(self.kit.provider.effect_count(OperationKind.AUTHORIZE), 1)

    def test_concurrent_operation_with_other_key_is_in_progress(self) -> None:
        self.kit.given_operation(self.payment_id, OperationKind.AUTHORIZE, "other-key")
        self.kit.payments.stale_events = 1

        with self.assertRaises(OperationInProgressError):
            self.handler.handle(self.authorize())

        self.assertEqual(self.kit.provider.requests, [])
        self.assertEqual(self.kit.payment_state(PaymentId(self.payment_id)), PaymentState.PENDING)


if __name__ == "__main__":
    unittest.main()
