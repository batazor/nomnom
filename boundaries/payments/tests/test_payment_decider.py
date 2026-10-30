"""``decide`` and ``evolve`` of a Payment as given/when/then over events (FR-740, FR-741)."""

import unittest
from dataclasses import dataclass
from datetime import UTC, datetime

from slices.payment.domains.payment import (
    CompleteOperation,
    CreatePayment,
    MarketId,
    Money,
    OperationId,
    OperationKind,
    OperationOutcome,
    OperationRequester,
    OperationState,
    OrderId,
    Payment,
    PaymentAuthorizationCancelled,
    PaymentAuthorized,
    PaymentCaptured,
    PaymentCommand,
    PaymentCreated,
    PaymentDomainEvent,
    PaymentFailed,
    PaymentId,
    PaymentModel,
    PaymentOperationDeclined,
    PaymentOperationRequested,
    PaymentOperationSucceeded,
    PaymentState,
    RequesterKind,
    RequestOperation,
    SealedSubject,
    crosses_snapshot_boundary,
    decide,
    evolve,
)

NOW = datetime(2026, 10, 25, 10, 0, tzinfo=UTC)
PAYMENT = PaymentId("payment-1")
ORDER = OrderId("order-1")
MARKET = MarketId("berlin")
AMOUNT = Money.parse("25.00", "EUR")
OPERATION = OperationId("op-1")
FACT = (PAYMENT, ORDER, MARKET, AMOUNT)
CREATED = PaymentCreated(*FACT)
REQUESTER = OperationRequester(
    RequesterKind.USER, SealedSubject("key-1", "nonce", "ciphertext"), "bff"
)


def requested(kind: OperationKind) -> PaymentOperationRequested:
    return PaymentOperationRequested(OPERATION, PAYMENT, kind, "key-1", AMOUNT, NOW, REQUESTER)


def fold(events: list[PaymentDomainEvent]) -> PaymentModel | None:
    state: PaymentModel | None = None
    for event in events:
        state = evolve(state, event)
    return state


@dataclass(frozen=True)
class Case:
    name: str
    given: list[PaymentDomainEvent]
    when: PaymentCommand
    then: list[PaymentDomainEvent]


CASES = [
    Case("AC-1020 create", [], CreatePayment(*FACT), [CREATED]),
    Case(
        "request",
        [CREATED],
        RequestOperation(OPERATION, OperationKind.AUTHORIZE, "key-1", NOW, REQUESTER),
        [requested(OperationKind.AUTHORIZE)],
    ),
    Case(
        "AC-1021 successful Authorization",
        [CREATED, requested(OperationKind.AUTHORIZE)],
        CompleteOperation(OPERATION, OperationOutcome.success(" auth-1 "), NOW),
        [
            PaymentOperationSucceeded(OPERATION, PAYMENT, OperationKind.AUTHORIZE, "auth-1", NOW),
            PaymentAuthorized(*FACT, "auth-1"),
        ],
    ),
    Case(
        "AC-1022 declined Authorization",
        [CREATED, requested(OperationKind.AUTHORIZE)],
        CompleteOperation(OPERATION, OperationOutcome.declined(" no funds "), NOW),
        [
            PaymentOperationDeclined(OPERATION, PAYMENT, OperationKind.AUTHORIZE, "no funds", NOW),
            PaymentFailed(PAYMENT, ORDER, MARKET, "no funds"),
        ],
    ),
    Case(
        "Capture without a Provider Reference",
        [CREATED, PaymentAuthorized(*FACT, "auth-1"), requested(OperationKind.CAPTURE)],
        CompleteOperation(OPERATION, OperationOutcome.success(), NOW),
        [
            PaymentOperationSucceeded(OPERATION, PAYMENT, OperationKind.CAPTURE, None, NOW),
            PaymentCaptured(*FACT),
        ],
    ),
    Case(
        "declined Capture keeps the Payment",
        [CREATED, PaymentAuthorized(*FACT, "auth-1"), requested(OperationKind.CAPTURE)],
        CompleteOperation(OPERATION, OperationOutcome.declined("expired"), NOW),
        [PaymentOperationDeclined(OPERATION, PAYMENT, OperationKind.CAPTURE, "expired", NOW)],
    ),
]


class DecideTest(unittest.TestCase):
    def test_given_events_when_command_then_events(self) -> None:
        for case in CASES:
            with self.subTest(case.name):
                self.assertEqual(decide(fold(case.given), case.when), case.then)

    def test_create_rejects_existing_payment_and_zero_amount(self) -> None:
        with self.assertRaises(ValueError):
            decide(fold([CREATED]), CreatePayment(*FACT))
        with self.assertRaises(ValueError):
            decide(None, CreatePayment(PAYMENT, ORDER, MARKET, Money.parse("0", "EUR")))

    def test_plain_user_identifier_is_rejected(self) -> None:
        plain = OperationRequester(RequesterKind.USER, "operator-1")
        with self.assertRaisesRegex(ValueError, "sealed"):
            decide(
                fold([CREATED]),
                RequestOperation(OPERATION, OperationKind.AUTHORIZE, "key-1", NOW, plain),
            )

    def test_commands_of_a_missing_payment_are_rejected(self) -> None:
        with self.assertRaises(ValueError):
            decide(
                None, RequestOperation(OPERATION, OperationKind.AUTHORIZE, "key-1", NOW, REQUESTER)
            )


class EvolveTest(unittest.TestCase):
    def test_ac_1017_state_is_the_fold_of_the_stream(self) -> None:
        state = fold(
            [
                CREATED,
                requested(OperationKind.AUTHORIZE),
                PaymentOperationSucceeded(
                    OPERATION, PAYMENT, OperationKind.AUTHORIZE, "auth-1", NOW
                ),
                PaymentAuthorized(*FACT, "auth-1"),
            ]
        )
        assert state is not None
        self.assertEqual(state.state, PaymentState.AUTHORIZED)
        self.assertEqual(state.provider_reference, "auth-1")
        self.assertEqual(state.operations[0].state, OperationState.SUCCEEDED)
        self.assertEqual(state.operations[0].provider_reference, "auth-1")

    def test_operation_keeps_who_requested_it(self) -> None:
        state = fold([CREATED, requested(OperationKind.AUTHORIZE)])
        assert state is not None
        self.assertEqual(state.operations[0].requested_by, REQUESTER)

    def test_event_recorded_before_requested_by_replays_as_unknown(self) -> None:
        old = PaymentOperationRequested(
            OPERATION, PAYMENT, OperationKind.AUTHORIZE, "key-1", AMOUNT, NOW, None
        )
        state = fold([CREATED, old])
        assert state is not None
        self.assertIsNone(state.operations[0].requested_by)

    def test_ac_1023_cancelled_authorization_clears_provider_reference(self) -> None:
        state = fold(
            [CREATED, PaymentAuthorized(*FACT, "auth-1"), PaymentAuthorizationCancelled(*FACT)]
        )
        assert state is not None
        self.assertEqual((state.state, state.provider_reference), (PaymentState.PENDING, None))

    def test_facts_replay_even_if_the_rules_would_reject_them(self) -> None:
        # evolve не проверяет правил: записанный факт применяется и после смены FSM.
        state = fold(
            [CREATED, PaymentAuthorized(*FACT, "auth-1"), PaymentAuthorized(*FACT, "auth-2")]
        )
        assert state is not None
        self.assertEqual(state.provider_reference, "auth-2")

    def test_corrupted_streams_are_rejected(self) -> None:
        with self.assertRaises(ValueError):
            evolve(None, requested(OperationKind.AUTHORIZE))
        with self.assertRaises(ValueError):
            evolve(fold([CREATED]), CREATED)
        with self.assertRaises(ValueError):
            evolve(
                fold([CREATED]),
                PaymentOperationSucceeded(OPERATION, PAYMENT, OperationKind.CAPTURE, None, NOW),
            )


class ReplayTest(unittest.TestCase):
    def test_ac_1019_replay_records_nothing_new(self) -> None:
        payment = Payment.from_history(None, 0, [CREATED, PaymentAuthorized(*FACT, "auth-1")])

        self.assertEqual(payment.version, 2)
        self.assertEqual(payment.state, PaymentState.AUTHORIZED)
        self.assertEqual(payment.uncommitted_events, ())
        self.assertEqual(payment.pull_events(), [])
        self.assertEqual(payment.pull_transitions(), [])

    def test_snapshot_and_tail_equal_the_full_history(self) -> None:
        events: list[PaymentDomainEvent] = [
            CREATED,
            PaymentAuthorized(*FACT, "auth-1"),
            PaymentCaptured(*FACT),
        ]
        full = Payment.from_history(None, 0, events)
        from_snapshot = Payment.from_history(fold(events[:2]), 2, events[2:])

        self.assertEqual(from_snapshot.model, full.model)
        self.assertEqual(from_snapshot.version, full.version)

    def test_empty_history_is_an_error(self) -> None:
        with self.assertRaises(ValueError):
            Payment.from_history(None, 0, [])

    def test_new_payment_waits_for_the_repository(self) -> None:
        payment = Payment(*FACT)

        self.assertEqual(payment.version, 0)
        self.assertEqual(payment.uncommitted_events, (CREATED,))
        payment.mark_committed(1)
        self.assertEqual(payment.version, 1)
        self.assertEqual(payment.uncommitted_events, ())


class SnapshotBoundaryTest(unittest.TestCase):
    def test_boundary(self) -> None:
        cases = [
            ("AC-1034 below", 2, 4, 5, False),
            ("reaches", 4, 5, 5, True),
            ("AC-1035 jumps over", 4, 6, 5, True),
            ("after the boundary", 5, 6, 5, False),
            ("next boundary", 9, 10, 5, True),
            ("first commit of every=1", 0, 1, 1, True),
        ]
        for name, old, new, every, expected in cases:
            with self.subTest(name):
                self.assertEqual(crosses_snapshot_boundary(old, new, every), expected)


if __name__ == "__main__":
    unittest.main()
