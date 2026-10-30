import unittest
from datetime import UTC, datetime
from unittest import mock

from slices.payment.domains.payment import (
    OPERATION_KINDS,
    AlreadyInStateError,
    GuardFailedError,
    InvalidOperationTransitionError,
    InvalidTransitionError,
    MarketId,
    Money,
    OperationAlreadyCompletedError,
    OperationAlreadyPendingError,
    OperationId,
    OperationKind,
    OperationMismatchError,
    OperationOutcome,
    OperationState,
    OperationTrigger,
    OrderId,
    Payment,
    PaymentAuthorizationCancelled,
    PaymentAuthorized,
    PaymentCaptured,
    PaymentFailed,
    PaymentId,
    PaymentModel,
    PaymentOperation,
    PaymentOperationDeclined,
    PaymentOperationSucceeded,
    PaymentRefunded,
    PaymentState,
    UnknownOperationKindError,
)
from slices.payment.domains.payment.operation import OPERATION_TRANSITIONS

NOW = datetime(2026, 10, 25, 10, 0, tzinfo=UTC)
AMOUNT = Money.parse("10.00", "EUR")
PAYMENT_ID = PaymentId("payment-1")
OPERATION_ID = OperationId("op-1")


def make_payment(state: PaymentState = PaymentState.PENDING) -> Payment:
    reference = None if state in {PaymentState.PENDING, PaymentState.FAILED} else "auth-1"
    return Payment.from_history(
        PaymentModel(PAYMENT_ID, OrderId("order-1"), MarketId("berlin"), AMOUNT, state, reference),
        version=1,
        events=[],
    )


def with_pending(kind: OperationKind, state: PaymentState) -> Payment:
    """Payment of ``state`` holding a pending operation of ``kind`` in the state machine's way."""
    operation = PaymentOperation(OPERATION_ID, PAYMENT_ID, kind, "key-1", AMOUNT, NOW)
    base = make_payment(state).model
    model = PaymentModel(
        base.id,
        base.order_id,
        base.market_id,
        base.amount,
        base.state,
        base.provider_reference,
        (operation,),
    )
    return Payment.from_history(model, version=2, events=[])


class PaymentOperationTest(unittest.TestCase):
    def test_transition_table_is_complete(self) -> None:
        expected = {(state, trigger) for state in OperationState for trigger in OperationTrigger}
        self.assertEqual(set(OPERATION_TRANSITIONS), expected)

    def test_requires_idempotency_key_and_consistent_result(self) -> None:
        with self.assertRaises(ValueError):
            PaymentOperation(OPERATION_ID, PAYMENT_ID, OperationKind.CAPTURE, " ", AMOUNT, NOW)
        with self.assertRaises(ValueError):
            PaymentOperation(
                OPERATION_ID,
                PAYMENT_ID,
                OperationKind.CAPTURE,
                "key",
                AMOUNT,
                NOW,
                OperationState.DECLINED,
                completed_at=NOW,
            )
        with self.assertRaises(ValueError):
            PaymentOperation(
                OPERATION_ID,
                PAYMENT_ID,
                OperationKind.CAPTURE,
                "key",
                AMOUNT,
                NOW,
                OperationState.SUCCEEDED,
            )

    def test_is_for_matches_payment_and_kind(self) -> None:
        operation = PaymentOperation(
            OPERATION_ID, PAYMENT_ID, OperationKind.CAPTURE, "key", AMOUNT, NOW
        )

        self.assertTrue(operation.is_for(PaymentId("payment-1"), OperationKind.CAPTURE))
        self.assertFalse(operation.is_for(PaymentId("payment-1"), OperationKind.REFUND))
        self.assertFalse(operation.is_for(PaymentId("payment-2"), OperationKind.CAPTURE))


class RequestOperationTest(unittest.TestCase):
    def test_records_a_pending_operation(self) -> None:
        payment = make_payment()

        operation = payment.request_operation(OPERATION_ID, OperationKind.AUTHORIZE, "key-1", NOW)

        self.assertEqual(operation.state, OperationState.PENDING)
        self.assertEqual(operation.amount, AMOUNT)
        self.assertEqual(payment.model.operation_by_key("key-1"), operation)
        self.assertTrue(payment.model.has_pending_operation)

    def test_rejects_second_pending_operation(self) -> None:
        payment = with_pending(OperationKind.CAPTURE, PaymentState.AUTHORIZED)

        with self.assertRaises(OperationAlreadyPendingError):
            payment.request_operation(
                OperationId("op-2"), OperationKind.CANCEL_AUTHORIZATION, "key-2", NOW
            )

    def test_rejects_blank_key(self) -> None:
        with self.assertRaises(ValueError):
            make_payment().request_operation(OPERATION_ID, OperationKind.AUTHORIZE, " ", NOW)


class CompleteOperationTest(unittest.TestCase):
    def test_successful_outcomes_apply_matching_payment_transition(self) -> None:
        cases = [
            (
                OperationKind.AUTHORIZE,
                PaymentState.PENDING,
                PaymentState.AUTHORIZED,
                PaymentAuthorized,
            ),
            (
                OperationKind.CAPTURE,
                PaymentState.AUTHORIZED,
                PaymentState.CAPTURED,
                PaymentCaptured,
            ),
            (
                OperationKind.CANCEL_AUTHORIZATION,
                PaymentState.AUTHORIZED,
                PaymentState.PENDING,
                PaymentAuthorizationCancelled,
            ),
            (OperationKind.REFUND, PaymentState.CAPTURED, PaymentState.REFUNDED, PaymentRefunded),
        ]
        for kind, from_state, to_state, event_type in cases:
            with self.subTest(kind=kind):
                payment = with_pending(kind, from_state)

                events = payment.complete_operation(
                    OPERATION_ID, OperationOutcome.success("ref-1"), NOW
                )

                self.assertEqual(payment.state, to_state)
                self.assertEqual(payment.operations[0].state, OperationState.SUCCEEDED)
                self.assertEqual(
                    [type(event) for event in events], [PaymentOperationSucceeded, event_type]
                )

    def test_declined_authorization_fails_payment(self) -> None:
        payment = with_pending(OperationKind.AUTHORIZE, PaymentState.PENDING)

        events = payment.complete_operation(
            OPERATION_ID, OperationOutcome.declined("card_declined"), NOW
        )

        self.assertEqual(payment.state, PaymentState.FAILED)
        self.assertEqual(payment.operations[0].state, OperationState.DECLINED)
        self.assertEqual(payment.operations[0].completed_at, NOW)
        self.assertEqual(
            events,
            [
                PaymentOperationDeclined(
                    OPERATION_ID, payment.id, OperationKind.AUTHORIZE, "card_declined", NOW
                ),
                PaymentFailed(payment.id, payment.order_id, payment.market_id, "card_declined"),
            ],
        )

    def test_other_declines_keep_payment_state(self) -> None:
        cases = [
            (OperationKind.CAPTURE, PaymentState.AUTHORIZED),
            (OperationKind.CANCEL_AUTHORIZATION, PaymentState.AUTHORIZED),
            (OperationKind.REFUND, PaymentState.CAPTURED),
        ]
        for kind, state in cases:
            with self.subTest(kind=kind):
                payment = with_pending(kind, state)

                events = payment.complete_operation(
                    OPERATION_ID, OperationOutcome.declined("provider_declined"), NOW
                )

                self.assertEqual(payment.state, state)
                self.assertEqual([type(event) for event in events], [PaymentOperationDeclined])

    def test_rejects_inadmissible_or_inconsistent_completion(self) -> None:
        with self.assertRaises(InvalidTransitionError):
            with_pending(OperationKind.CAPTURE, PaymentState.PENDING).complete_operation(
                OPERATION_ID, OperationOutcome.success(), NOW
            )
        with self.assertRaises(AlreadyInStateError):
            with_pending(OperationKind.CAPTURE, PaymentState.CAPTURED).complete_operation(
                OPERATION_ID, OperationOutcome.declined("late"), NOW
            )
        with self.assertRaises(OperationMismatchError):
            make_payment().complete_operation(
                OperationId("other"), OperationOutcome.success("ref"), NOW
            )

    def test_failed_guard_leaves_operation_pending(self) -> None:
        payment = with_pending(OperationKind.AUTHORIZE, PaymentState.PENDING)

        with self.assertRaises(GuardFailedError):
            payment.complete_operation(OPERATION_ID, OperationOutcome.success(None), NOW)

        self.assertEqual(payment.operations[0].state, OperationState.PENDING)
        self.assertEqual(payment.state, PaymentState.PENDING)
        self.assertEqual(payment.uncommitted_events, ())

    def test_completed_operation_is_not_completed_again(self) -> None:
        payment = with_pending(OperationKind.CAPTURE, PaymentState.AUTHORIZED)
        payment.complete_operation(OPERATION_ID, OperationOutcome.success(), NOW)

        with self.assertRaises(OperationAlreadyCompletedError):
            payment.complete_operation(OPERATION_ID, OperationOutcome.success(), NOW)
        with self.assertRaises(InvalidOperationTransitionError):
            payment.complete_operation(OPERATION_ID, OperationOutcome.declined("late"), NOW)


class OperationKindRulesTest(unittest.TestCase):
    def test_every_kind_has_a_rule(self) -> None:
        self.assertEqual(set(OPERATION_KINDS), set(OperationKind))

    def test_kind_without_rule_fails_and_changes_nothing(self) -> None:
        for outcome in (OperationOutcome.success("ref-1"), OperationOutcome.declined("no")):
            with self.subTest(succeeded=outcome.succeeded):
                payment = with_pending(OperationKind.REFUND, PaymentState.CAPTURED)

                with (
                    mock.patch.dict(OPERATION_KINDS, clear=True),
                    self.assertRaises(UnknownOperationKindError),
                ):
                    payment.complete_operation(OPERATION_ID, outcome, NOW)

                self.assertEqual(payment.state, PaymentState.CAPTURED)
                self.assertEqual(payment.pull_events(), [])
                self.assertEqual(payment.operations[0].state, OperationState.PENDING)


if __name__ == "__main__":
    unittest.main()
