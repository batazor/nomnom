import unittest
from typing import Any

from slices.payment.domains.payment.events import (
    PaymentAuthorizationCancelled,
    PaymentAuthorized,
    PaymentCaptured,
    PaymentFailed,
    PaymentRefunded,
)
from slices.payment.domains.payment.fsm import (
    _TRANSITION_TABLE,
    AlreadyInState,
    GuardFailed,
    InvalidTransition,
    PaymentState,
    PaymentTrigger,
)
from slices.payment.domains.payment.ids import OrderId, PaymentId
from slices.payment.domains.payment.payment import Payment
from slices.payment.domains.payment.vo.money import CurrencyMismatch, InvalidMoney, Money


class PaymentDomainTest(unittest.TestCase):
    def test_transition_table_is_complete(self) -> None:
        expected_pairs = {(state, trigger) for state in PaymentState for trigger in PaymentTrigger}
        self.assertEqual(set(_TRANSITION_TABLE), expected_pairs)

    def test_payment_lifecycle_is_strict(self) -> None:
        payment = self.payment()

        authorized = payment.authorize(" provider-1 ")
        self.assertEqual(authorized.to_state, PaymentState.AUTHORIZED)
        self.assertEqual(payment.provider_reference, "provider-1")

        with self.assertRaises(AlreadyInState) as duplicate:
            payment.authorize("provider-1")
        self.assertEqual(duplicate.exception.state, PaymentState.AUTHORIZED)

        self.assertEqual(payment.capture().to_state, PaymentState.CAPTURED)
        self.assertEqual(payment.refund().to_state, PaymentState.REFUNDED)
        payment_id, order_id, amount = PaymentId("payment-1"), OrderId("order-1"), payment.amount
        self.assertEqual(
            payment.pull_events(),
            [
                PaymentAuthorized(payment_id, order_id, amount),
                PaymentCaptured(payment_id, order_id, amount),
                PaymentRefunded(payment_id, order_id, amount),
            ],
        )
        self.assertEqual(payment.pull_events(), [])

        with self.assertRaises(AlreadyInState):
            payment.refund()
        self.assertEqual(payment.pull_events(), [])

    def test_authorization_guard_requires_provider_reference(self) -> None:
        payment = self.payment()
        with self.assertRaises(GuardFailed) as raised:
            payment.authorize("  ")
        self.assertEqual(raised.exception.reason, "provider_reference_required")
        self.assertEqual(payment.state, PaymentState.PENDING)

    def test_cancelling_authorization_returns_payment_to_pending(self) -> None:
        payment = self.payment()
        payment.authorize("provider-1")

        result = payment.cancel_authorization()

        self.assertEqual(result.to_state, PaymentState.PENDING)
        self.assertIsNone(payment.provider_reference)
        self.assertEqual(
            payment.pull_events()[-1],
            PaymentAuthorizationCancelled(payment.id, payment.order_id, payment.amount),
        )
        with self.assertRaises(AlreadyInState):
            payment.cancel_authorization()

    def test_invalid_transition_does_not_change_state(self) -> None:
        payment = self.payment()
        with self.assertRaises(InvalidTransition) as raised:
            payment.capture()
        self.assertEqual(raised.exception.from_state, PaymentState.PENDING)
        self.assertEqual(raised.exception.trigger, PaymentTrigger.CAPTURE)
        self.assertEqual(payment.state, PaymentState.PENDING)

    def test_ensure_can_checks_table_without_changing_state(self) -> None:
        payment = self.payment()

        payment.ensure_can(PaymentTrigger.AUTHORIZE)
        with self.assertRaises(InvalidTransition):
            payment.ensure_can(PaymentTrigger.CAPTURE)
        with self.assertRaises(AlreadyInState):
            payment.ensure_can(PaymentTrigger.CANCEL_AUTHORIZATION)
        self.assertEqual(payment.state, PaymentState.PENDING)
        self.assertEqual(payment.pull_events(), [])

    def test_fail_records_reason(self) -> None:
        payment = self.payment()

        self.assertEqual(payment.fail(" card_declined ").to_state, PaymentState.FAILED)
        self.assertEqual(
            payment.pull_events(), [PaymentFailed(payment.id, payment.order_id, "card_declined")]
        )
        with self.assertRaises(ValueError):
            self.payment().fail(" ")

    def test_money_is_exact_positive_and_single_currency(self) -> None:
        first = Money.parse("0.10", "EUR")
        second = Money.parse("0.20", "EUR")
        self.assertEqual(first.add(second), Money.parse("0.30", "EUR"))

        floating_amount: Any = 0.1
        with self.assertRaises(InvalidMoney):
            Money.parse(floating_amount, "EUR")
        for not_finite in ("NaN", "Infinity"):
            with self.assertRaises(InvalidMoney):
                Money.parse(not_finite, "EUR")
        with self.assertRaises(CurrencyMismatch):
            first.add(Money.parse("1", "USD"))
        with self.assertRaises(ValueError):
            Payment(PaymentId("payment-2"), OrderId("order-1"), Money.parse("0", "EUR"))

    @staticmethod
    def payment() -> Payment:
        return Payment(
            PaymentId("payment-1"),
            OrderId("order-1"),
            Money.parse("10.00", "EUR"),
        )


if __name__ == "__main__":
    unittest.main()
