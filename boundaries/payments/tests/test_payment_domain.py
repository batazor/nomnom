import unittest
from datetime import UTC, datetime
from typing import Any

from slices.payment.domains.payment.events import (
    PaymentAuthorizationCancelled,
    PaymentAuthorized,
    PaymentDomainEvent,
    PaymentFailed,
)
from slices.payment.domains.payment.fsm import (
    PARENT,
    REPEATS,
    TRANSITIONS,
    AlreadyInStateError,
    GuardFailedError,
    InvalidTransitionError,
    PaymentCompoundState,
    PaymentState,
    PaymentTrigger,
    TransitionInput,
    is_in,
    leaves_of,
    lineage,
    transition,
)
from slices.payment.domains.payment.ids import OperationId, OperationKind, OrderId, PaymentId
from slices.payment.domains.payment.model import PaymentModel
from slices.payment.domains.payment.operation import OperationOutcome
from slices.payment.domains.payment.payment import Payment
from slices.payment.domains.payment.vo.market import InvalidMarketIdError, MarketId
from slices.payment.domains.payment.vo.money import CurrencyMismatchError, InvalidMoneyError, Money

NOW = datetime(2026, 10, 25, 10, 0, tzinfo=UTC)
_S = PaymentState
_T = PaymentTrigger
_C = PaymentCompoundState
_ALREADY = "already"
_INVALID = "invalid"

# Complete expected matrix of ``state x trigger``, written independently of the table.
_EXPECTED: dict[PaymentState, dict[PaymentTrigger, PaymentState | str]] = {
    _S.PENDING: {
        _T.AUTHORIZE: _S.AUTHORIZED,
        _T.CANCEL_AUTHORIZATION: _ALREADY,
        _T.CAPTURE: _INVALID,
        _T.REFUND: _INVALID,
        _T.FAIL: _S.FAILED,
    },
    _S.AUTHORIZED: {
        _T.AUTHORIZE: _ALREADY,
        _T.CANCEL_AUTHORIZATION: _S.PENDING,
        _T.CAPTURE: _S.CAPTURED,
        _T.REFUND: _INVALID,
        _T.FAIL: _S.FAILED,
    },
    _S.CAPTURED: {
        _T.AUTHORIZE: _INVALID,
        _T.CANCEL_AUTHORIZATION: _INVALID,
        _T.CAPTURE: _ALREADY,
        _T.REFUND: _S.REFUNDED,
        _T.FAIL: _INVALID,
    },
    _S.REFUNDED: {
        _T.AUTHORIZE: _INVALID,
        _T.CANCEL_AUTHORIZATION: _INVALID,
        _T.CAPTURE: _INVALID,
        _T.REFUND: _ALREADY,
        _T.FAIL: _INVALID,
    },
    _S.FAILED: {
        _T.AUTHORIZE: _INVALID,
        _T.CANCEL_AUTHORIZATION: _INVALID,
        _T.CAPTURE: _INVALID,
        _T.REFUND: _INVALID,
        _T.FAIL: _ALREADY,
    },
}

# Input that satisfies every guard.
_PASSING_INPUT = TransitionInput(provider_reference="provider-1")


class PaymentStateMachineTest(unittest.TestCase):
    def test_every_state_trigger_pair_has_the_expected_outcome(self) -> None:
        self.assertEqual(set(_EXPECTED), set(PaymentState))
        for state in PaymentState:
            self.assertEqual(set(_EXPECTED[state]), set(PaymentTrigger), state)
            for trigger in PaymentTrigger:
                expected = _EXPECTED[state][trigger]
                with self.subTest(state=state, trigger=trigger):
                    if expected == _ALREADY:
                        with self.assertRaises(AlreadyInStateError) as already:
                            transition(state, trigger, _PASSING_INPUT)
                        self.assertEqual(already.exception.state, state)
                    elif expected == _INVALID:
                        with self.assertRaises(InvalidTransitionError) as invalid:
                            transition(state, trigger, _PASSING_INPUT)
                        self.assertEqual(
                            (invalid.exception.from_state, invalid.exception.trigger),
                            (state, trigger),
                        )
                    else:
                        result = transition(state, trigger, _PASSING_INPUT)
                        self.assertEqual(
                            (result.from_state, result.trigger, result.to_state),
                            (state, trigger, expected),
                        )

    def test_hierarchy_is_acyclic_and_every_compound_state_has_children(self) -> None:
        self.assertEqual(set(PARENT), set(PaymentState) | set(PaymentCompoundState))
        self.assertTrue(
            {state.value for state in PaymentState}.isdisjoint(
                compound.value for compound in PaymentCompoundState
            )
        )
        for node in PARENT:
            nodes = lineage(node)
            self.assertEqual(len(set(nodes)), len(nodes), node)
        for compound in PaymentCompoundState:
            self.assertIn(compound, PARENT.values())
            self.assertTrue(leaves_of(compound), compound)

    def test_table_declares_each_node_trigger_once_and_repeats_only_on_leaves(self) -> None:
        declared = [(row.source, row.trigger) for row in TRANSITIONS]
        self.assertEqual(len(set(declared)), len(declared))
        self.assertEqual(len(set(REPEATS)), len(REPEATS))
        for state, trigger in REPEATS:
            self.assertIsInstance(state, PaymentState)
            self.assertNotIn((state, trigger), declared)

    def test_compound_states_group_the_expected_leaf_states(self) -> None:
        self.assertEqual(leaves_of(_C.UNCAPTURED), (_S.PENDING, _S.AUTHORIZED))
        self.assertEqual(leaves_of(_C.CLOSED), (_S.REFUNDED, _S.FAILED))
        self.assertTrue(is_in(_S.AUTHORIZED, _C.UNCAPTURED))
        self.assertFalse(is_in(_S.CAPTURED, _C.UNCAPTURED))
        self.assertFalse(is_in(_S.CAPTURED, _C.CLOSED))


def run_operation(
    payment: Payment,
    kind: OperationKind,
    outcome: OperationOutcome,
    operation_id: str | None = None,
) -> list[PaymentDomainEvent]:
    target = OperationId(operation_id or f"op-{kind.value}")
    payment.request_operation(target, kind, f"key-{target}", NOW)
    payment.pull_events()
    return payment.complete_operation(target, outcome, NOW)


class PaymentDomainTest(unittest.TestCase):
    def test_payment_lifecycle_is_strict(self) -> None:
        payment = self.payment()
        payment.pull_events()

        run_operation(payment, OperationKind.AUTHORIZE, OperationOutcome.success(" provider-1 "))
        self.assertEqual(payment.state, PaymentState.AUTHORIZED)
        self.assertEqual(payment.provider_reference, "provider-1")
        self.assertEqual(
            [t.to_state for t in payment.pull_transitions()], [PaymentState.AUTHORIZED]
        )

        with self.assertRaises(AlreadyInStateError) as duplicate:
            payment.request_operation(OperationId("op-2"), OperationKind.AUTHORIZE, "key-2", NOW)
        self.assertEqual(duplicate.exception.state, PaymentState.AUTHORIZED)

        run_operation(payment, OperationKind.CAPTURE, OperationOutcome.success())
        self.assertEqual(payment.state, PaymentState.CAPTURED)
        run_operation(payment, OperationKind.REFUND, OperationOutcome.success())
        self.assertEqual(payment.state, PaymentState.REFUNDED)
        self.assertEqual(payment.pull_events(), [])

        with self.assertRaises(AlreadyInStateError):
            payment.request_operation(OperationId("op-3"), OperationKind.REFUND, "key-3", NOW)
        self.assertEqual(payment.pull_events(), [])

    def test_authorization_guard_requires_provider_reference(self) -> None:
        payment = self.payment()
        operation = payment.request_operation(
            OperationId("op-1"), OperationKind.AUTHORIZE, "key-1", NOW
        )
        with self.assertRaises(GuardFailedError) as raised:
            payment.complete_operation(operation.id, OperationOutcome.success("  "), NOW)
        self.assertEqual(raised.exception.reason, "provider_reference_required")
        self.assertEqual(payment.state, PaymentState.PENDING)

    def test_cancelling_authorization_returns_payment_to_pending(self) -> None:
        payment = self.payment()
        run_operation(payment, OperationKind.AUTHORIZE, OperationOutcome.success("provider-1"))

        events = run_operation(
            payment, OperationKind.CANCEL_AUTHORIZATION, OperationOutcome.success()
        )

        self.assertEqual(payment.state, PaymentState.PENDING)
        self.assertIsNone(payment.provider_reference)
        self.assertEqual(
            events[-1],
            PaymentAuthorizationCancelled(
                payment.id, payment.order_id, payment.market_id, payment.amount
            ),
        )
        with self.assertRaises(AlreadyInStateError):
            payment.request_operation(
                OperationId("op-2"), OperationKind.CANCEL_AUTHORIZATION, "key-2", NOW
            )

    def test_invalid_transition_does_not_change_state(self) -> None:
        payment = self.payment()
        with self.assertRaises(InvalidTransitionError) as raised:
            payment.request_operation(OperationId("op-1"), OperationKind.CAPTURE, "key-1", NOW)
        self.assertEqual(raised.exception.from_state, PaymentState.PENDING)
        self.assertEqual(raised.exception.trigger, PaymentTrigger.CAPTURE)
        self.assertEqual(payment.state, PaymentState.PENDING)
        self.assertEqual(payment.operations, ())

    def test_declined_authorization_records_reason(self) -> None:
        payment = self.payment()

        events = run_operation(
            payment, OperationKind.AUTHORIZE, OperationOutcome.declined(" card_declined ")
        )

        self.assertEqual(payment.state, PaymentState.FAILED)
        self.assertEqual(
            events[-1],
            PaymentFailed(payment.id, payment.order_id, payment.market_id, "card_declined"),
        )

    def test_successful_state_requires_provider_reference(self) -> None:
        with self.assertRaises(ValueError):
            PaymentModel(
                PaymentId("payment-1"),
                OrderId("order-1"),
                MarketId("berlin"),
                Money.parse("10.00", "EUR"),
                state=PaymentState.AUTHORIZED,
            )

    def test_money_is_exact_positive_and_single_currency(self) -> None:
        first = Money.parse("0.10", "EUR")
        second = Money.parse("0.20", "EUR")
        self.assertEqual(first.add(second), Money.parse("0.30", "EUR"))

        floating_amount: Any = 0.1
        with self.assertRaises(InvalidMoneyError):
            Money.parse(floating_amount, "EUR")
        for not_finite in ("NaN", "Infinity"):
            with self.assertRaises(InvalidMoneyError):
                Money.parse(not_finite, "EUR")
        with self.assertRaises(CurrencyMismatchError):
            first.add(Money.parse("1", "USD"))
        with self.assertRaises(ValueError):
            Payment(
                PaymentId("payment-2"),
                OrderId("order-1"),
                MarketId("berlin"),
                Money.parse("0", "EUR"),
            )

    def test_market_id_is_a_slug(self) -> None:
        for valid in ("berlin", "new-york", "b2", "a" + "b" * 31):
            with self.subTest(market_id=valid):
                self.assertEqual(str(MarketId(valid)), valid)
        for invalid in ("", "b", "Berlin", "1berlin", "-berlin", "berlin city", "a" + "b" * 32):
            with self.subTest(market_id=invalid), self.assertRaises(InvalidMarketIdError):
                MarketId(invalid)

    def test_market_is_kept_from_creation(self) -> None:
        payment = self.payment()

        events = run_operation(
            payment, OperationKind.AUTHORIZE, OperationOutcome.success("provider-1")
        )

        self.assertEqual(payment.market_id, MarketId("berlin"))
        authorized = events[-1]
        assert isinstance(authorized, PaymentAuthorized)
        self.assertEqual(authorized.market_id, MarketId("berlin"))

    @staticmethod
    def payment() -> Payment:
        return Payment(
            PaymentId("payment-1"),
            OrderId("order-1"),
            MarketId("berlin"),
            Money.parse("10.00", "EUR"),
        )


if __name__ == "__main__":
    unittest.main()
