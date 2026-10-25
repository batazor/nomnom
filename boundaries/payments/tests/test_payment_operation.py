import unittest
from datetime import UTC, datetime

from slices.payment.domains.payment import (
    AlreadyInState,
    GuardFailed,
    InvalidOperationTransition,
    InvalidTransition,
    Money,
    OperationAlreadyCompleted,
    OperationId,
    OperationKind,
    OperationMismatch,
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
    PaymentOperation,
    PaymentOperationDeclined,
    PaymentRefunded,
    PaymentState,
    complete_operation,
)
from slices.payment.domains.payment.operation import OPERATION_TRANSITIONS

NOW = datetime(2026, 10, 25, 10, 0, tzinfo=UTC)
AMOUNT = Money.parse("10.00", "EUR")
PAYMENT_ID = PaymentId("payment-1")


def make_payment(state: PaymentState = PaymentState.PENDING) -> Payment:
    reference = None if state in {PaymentState.PENDING, PaymentState.FAILED} else "auth-1"
    return Payment.restore(
        PaymentId("payment-1"), OrderId("order-1"), AMOUNT, state, reference, version=1
    )


def make_operation(kind: OperationKind, payment_id: PaymentId = PAYMENT_ID) -> PaymentOperation:
    return PaymentOperation(OperationId("op-1"), payment_id, kind, "key-1", AMOUNT, NOW)


class PaymentOperationLifecycleTest(unittest.TestCase):
    def test_transition_table_is_complete(self) -> None:
        expected = {(state, trigger) for state in OperationState for trigger in OperationTrigger}
        self.assertEqual(set(OPERATION_TRANSITIONS), expected)

    def test_succeed_is_strict(self) -> None:
        operation = make_operation(OperationKind.CAPTURE)

        operation.succeed(" capture-1 ", NOW)

        self.assertEqual(operation.state, OperationState.SUCCEEDED)
        self.assertEqual(operation.provider_reference, "capture-1")
        self.assertEqual(operation.completed_at, NOW)
        with self.assertRaises(OperationAlreadyCompleted):
            operation.succeed("capture-1", NOW)
        with self.assertRaises(InvalidOperationTransition):
            operation.decline("late", NOW)

    def test_decline_records_event_once(self) -> None:
        operation = make_operation(OperationKind.REFUND)

        operation.decline(" insufficient_funds ", NOW)

        self.assertEqual(operation.decline_reason, "insufficient_funds")
        self.assertEqual(
            operation.pull_events(),
            [
                PaymentOperationDeclined(
                    operation.id, operation.payment_id, OperationKind.REFUND, "insufficient_funds"
                )
            ],
        )
        self.assertEqual(operation.pull_events(), [])
        with self.assertRaises(OperationAlreadyCompleted):
            operation.decline("again", NOW)
        with self.assertRaises(InvalidOperationTransition):
            operation.succeed(None, NOW)

    def test_requires_idempotency_key_and_valid_restore(self) -> None:
        with self.assertRaises(ValueError):
            PaymentOperation(
                OperationId("op-1"), PaymentId("p"), OperationKind.CAPTURE, " ", AMOUNT, NOW
            )
        with self.assertRaises(ValueError):
            PaymentOperation.restore(
                OperationId("op-1"),
                PaymentId("p"),
                OperationKind.CAPTURE,
                "key",
                AMOUNT,
                NOW,
                OperationState.DECLINED,
                None,
                None,
                NOW,
                1,
            )

    def test_is_for_matches_payment_and_kind(self) -> None:
        operation = make_operation(OperationKind.CAPTURE)

        self.assertTrue(operation.is_for(PaymentId("payment-1"), OperationKind.CAPTURE))
        self.assertFalse(operation.is_for(PaymentId("payment-1"), OperationKind.REFUND))
        self.assertFalse(operation.is_for(PaymentId("payment-2"), OperationKind.CAPTURE))


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
                payment = make_payment(from_state)
                operation = make_operation(kind)

                events = complete_operation(
                    payment, operation, OperationOutcome.success("ref-1"), NOW
                )

                self.assertEqual(payment.state, to_state)
                self.assertEqual(operation.state, OperationState.SUCCEEDED)
                self.assertEqual([type(event) for event in events], [event_type])

    def test_declined_authorization_fails_payment(self) -> None:
        payment = make_payment()
        operation = make_operation(OperationKind.AUTHORIZE)

        events = complete_operation(
            payment, operation, OperationOutcome.declined("card_declined"), NOW
        )

        self.assertEqual(payment.state, PaymentState.FAILED)
        self.assertEqual(operation.state, OperationState.DECLINED)
        self.assertEqual(
            events,
            [
                PaymentOperationDeclined(
                    operation.id, payment.id, OperationKind.AUTHORIZE, "card_declined"
                ),
                PaymentFailed(payment.id, payment.order_id, "card_declined"),
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
                payment = make_payment(state)
                operation = make_operation(kind)

                events = complete_operation(
                    payment, operation, OperationOutcome.declined("provider_declined"), NOW
                )

                self.assertEqual(payment.state, state)
                self.assertEqual([type(event) for event in events], [PaymentOperationDeclined])

    def test_rejects_inadmissible_or_inconsistent_completion(self) -> None:
        with self.assertRaises(InvalidTransition):
            complete_operation(
                make_payment(PaymentState.PENDING),
                make_operation(OperationKind.CAPTURE),
                OperationOutcome.success(),
                NOW,
            )
        with self.assertRaises(AlreadyInState):
            complete_operation(
                make_payment(PaymentState.CAPTURED),
                make_operation(OperationKind.CAPTURE),
                OperationOutcome.declined("late"),
                NOW,
            )
        with self.assertRaises(OperationMismatch):
            complete_operation(
                make_payment(),
                make_operation(OperationKind.AUTHORIZE, PaymentId("other")),
                OperationOutcome.success("ref"),
                NOW,
            )

    def test_failed_guard_leaves_operation_pending(self) -> None:
        payment = make_payment()
        operation = make_operation(OperationKind.AUTHORIZE)

        with self.assertRaises(GuardFailed):
            complete_operation(payment, operation, OperationOutcome.success(None), NOW)

        self.assertEqual(operation.state, OperationState.PENDING)
        self.assertEqual(payment.state, PaymentState.PENDING)

    def test_completed_operation_is_not_completed_again(self) -> None:
        payment = make_payment(PaymentState.AUTHORIZED)
        operation = make_operation(OperationKind.CAPTURE)
        complete_operation(payment, operation, OperationOutcome.success(), NOW)

        with self.assertRaises(OperationAlreadyCompleted):
            complete_operation(payment, operation, OperationOutcome.success(), NOW)


if __name__ == "__main__":
    unittest.main()
