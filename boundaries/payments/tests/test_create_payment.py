import unittest
from typing import override

import pytest
from payment_fakes import (
    MARKET,
    ORCHESTRATOR,
    SNAPSHOT_EVERY,
    PaymentsTestKit,
)

from infrastructure.repository import Database
from pkg.authz import AuthorizationUnavailableError, PermissionDeniedError
from slices.payment.applications.create_payment import (
    CreatePaymentHandler,
    CreatePaymentInput,
)
from slices.payment.applications.errors import InvalidPaymentRequestError, PaymentAlreadyExistsError
from slices.payment.applications.ports import PaymentPermission, PaymentResource
from slices.payment.domains.payment import (
    Money,
    OrderId,
    Payment,
    PaymentId,
    PaymentState,
)
from slices.payment.infrastructure.repository import DatabasePaymentRepository


@pytest.mark.usefixtures("database")
class CreatePaymentTest(unittest.TestCase):
    database: Database

    @override
    def setUp(self) -> None:
        self.kit = PaymentsTestKit(self.database)
        self.handler = CreatePaymentHandler(self.kit.payments, self.kit.permissions, self.kit.ids)

    def create(
        self,
        order_id: str = "order-1",
        amount: str = "25.00",
        currency: str = "EUR",
        market_id: str = MARKET.value,
    ) -> CreatePaymentInput:
        return CreatePaymentInput(ORCHESTRATOR, order_id, market_id, amount, currency)

    def test_ac_400_creates_pending_payment_for_order(self) -> None:
        result = self.handler.handle(self.create())

        self.assertTrue(result.created)
        self.assertEqual(result.state, PaymentState.PENDING)
        stored = self.kit.payment(result.payment_id)
        self.assertEqual(stored.order_id, OrderId("order-1"))
        self.assertEqual(stored.market_id, MARKET)
        self.assertEqual(stored.amount, Money.parse("25.00", "EUR"))
        self.assertEqual(
            self.kit.permissions.checks[0][1:],
            (PaymentPermission.CREATE, PaymentResource(result.payment_id, OrderId("order-1"))),
        )
        self.assertEqual(self.kit.provider.requests, [])
        self.assertEqual(self.kit.published(), [])

    def test_ac_401_repeated_create_returns_existing_payment(self) -> None:
        first = self.handler.handle(self.create())

        second = self.handler.handle(self.create(amount="25.0"))

        self.assertFalse(second.created)
        self.assertEqual(second.payment_id, first.payment_id)
        self.assertEqual(len(self.kit.payment_ids()), 1)

    def test_ac_402_conflicting_terms_are_rejected(self) -> None:
        first = self.handler.handle(self.create())

        for amount, currency in (("30.00", "EUR"), ("25.00", "USD")):
            with (
                self.subTest(amount=amount, currency=currency),
                self.assertRaises(PaymentAlreadyExistsError),
            ):
                self.handler.handle(self.create(amount=amount, currency=currency))
        self.assertEqual(self.kit.payment_ids(), [first.payment_id])
        self.assertEqual(self.kit.payment(first.payment_id).version, 1)

    def test_repeated_create_in_another_market_is_rejected(self) -> None:
        first = self.handler.handle(self.create())

        with self.assertRaises(PaymentAlreadyExistsError):
            self.handler.handle(self.create(market_id="hamburg"))

        self.assertEqual(self.kit.payment(first.payment_id).market_id, MARKET)

    def test_empty_or_malformed_market_is_rejected(self) -> None:
        for market_id in ("", " ", "b", "Berlin", "1berlin", "berlin_city", "b" * 33):
            with (
                self.subTest(market_id=market_id),
                self.assertRaises(InvalidPaymentRequestError),
            ):
                self.handler.handle(self.create(market_id=market_id))
        self.assertEqual(self.kit.payment_ids(), [])
        self.assertEqual(self.kit.permissions.checks, [])

    def test_ac_403_invalid_amount_or_request_is_rejected(self) -> None:
        cases = [
            ("0", "EUR"),
            ("-1", "EUR"),
            ("ten", "EUR"),
            ("NaN", "EUR"),
            ("10", "eur"),
        ]
        for amount, currency in cases:
            with (
                self.subTest(amount=amount, currency=currency),
                self.assertRaises(InvalidPaymentRequestError),
            ):
                self.handler.handle(self.create(amount=amount, currency=currency))
        with self.assertRaises(InvalidPaymentRequestError):
            self.handler.handle(self.create(order_id=" "))
        self.assertEqual(self.kit.payment_ids(), [])

    def test_ac_426_denied_permission_creates_nothing(self) -> None:
        self.kit.permissions.denied.add(PaymentPermission.CREATE)

        with self.assertRaises(PermissionDeniedError):
            self.handler.handle(self.create())

        self.assertEqual(self.kit.payment_ids(), [])

    def test_ac_427_unavailable_authorization_fails_closed(self) -> None:
        self.kit.permissions.unavailable = True

        with self.assertRaises(AuthorizationUnavailableError):
            self.handler.handle(self.create())

        self.assertEqual(self.kit.payment_ids(), [])

    def test_concurrent_create_returns_payment_of_the_winner(self) -> None:
        winner = Payment(
            PaymentId("payment-winner"), OrderId("order-1"), MARKET, Money.parse("25", "EUR")
        )
        payments = RacingPaymentRepository(self.database, winner)
        handler = CreatePaymentHandler(payments, self.kit.permissions, self.kit.ids)

        result = handler.handle(self.create())

        self.assertFalse(result.created)
        self.assertEqual(result.payment_id, PaymentId("payment-winner"))
        self.assertEqual(self.kit.payment_ids(), [PaymentId("payment-winner")])


class RacingPaymentRepository(DatabasePaymentRepository):
    """Another request stores its Payment between the lookup and the insert."""

    def __init__(self, database: Database, winner: Payment) -> None:
        super().__init__(database, SNAPSHOT_EVERY)
        self._winner: Payment | None = winner

    @override
    def get_by_order(self, order_id: OrderId) -> Payment | None:
        found = super().get_by_order(order_id)
        if self._winner is not None:
            winner, self._winner = self._winner, None
            super().add(winner)
        return found


if __name__ == "__main__":
    unittest.main()
