import unittest

from payment_fakes import (
    ORCHESTRATOR,
    InMemoryPaymentRepository,
    InMemoryPaymentStore,
    PaymentsTestKit,
)

from pkg.application import AuthorizationUnavailable, PermissionDenied
from slices.payment.applications.create_payment import (
    CreatePaymentHandler,
    CreatePaymentInput,
)
from slices.payment.applications.errors import InvalidPaymentRequest, PaymentAlreadyExists
from slices.payment.applications.ports import PaymentPermission, PaymentResource
from slices.payment.domains.payment import (
    Money,
    OrderId,
    Payment,
    PaymentId,
    PaymentState,
)


class CreatePaymentTest(unittest.TestCase):
    def setUp(self) -> None:
        self.kit = PaymentsTestKit()
        self.handler = CreatePaymentHandler(self.kit.payments, self.kit.permissions, self.kit.ids)

    def create(
        self, order_id: str = "order-1", amount: str = "25.00", currency: str = "EUR"
    ) -> CreatePaymentInput:
        return CreatePaymentInput(ORCHESTRATOR, order_id, amount, currency)

    def test_ac_400_creates_pending_payment_for_order(self) -> None:
        result = self.handler.handle(self.create())

        self.assertTrue(result.created)
        self.assertEqual(result.state, PaymentState.PENDING)
        stored = self.kit.store.payments[result.payment_id]
        self.assertEqual(stored.order_id, OrderId("order-1"))
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
        self.assertEqual(len(self.kit.store.payments), 1)

    def test_ac_402_conflicting_terms_are_rejected(self) -> None:
        first = self.handler.handle(self.create())

        for amount, currency in (("30.00", "EUR"), ("25.00", "USD")):
            with self.subTest(amount=amount, currency=currency):
                with self.assertRaises(PaymentAlreadyExists):
                    self.handler.handle(self.create(amount=amount, currency=currency))
        self.assertEqual(list(self.kit.store.payments), [first.payment_id])
        self.assertEqual(self.kit.store.payments[first.payment_id].version, 1)

    def test_ac_403_invalid_amount_or_request_is_rejected(self) -> None:
        cases = [
            ("0", "EUR"),
            ("-1", "EUR"),
            ("ten", "EUR"),
            ("NaN", "EUR"),
            ("10", "eur"),
        ]
        for amount, currency in cases:
            with self.subTest(amount=amount, currency=currency):
                with self.assertRaises(InvalidPaymentRequest):
                    self.handler.handle(self.create(amount=amount, currency=currency))
        with self.assertRaises(InvalidPaymentRequest):
            self.handler.handle(self.create(order_id=" "))
        self.assertEqual(self.kit.store.payments, {})

    def test_ac_426_denied_permission_creates_nothing(self) -> None:
        self.kit.permissions.denied.add(PaymentPermission.CREATE)

        with self.assertRaises(PermissionDenied):
            self.handler.handle(self.create())

        self.assertEqual(self.kit.store.payments, {})

    def test_ac_427_unavailable_authorization_fails_closed(self) -> None:
        self.kit.permissions.unavailable = True

        with self.assertRaises(AuthorizationUnavailable):
            self.handler.handle(self.create())

        self.assertEqual(self.kit.store.payments, {})

    def test_concurrent_create_returns_payment_of_the_winner(self) -> None:
        winner = Payment(PaymentId("payment-winner"), OrderId("order-1"), Money.parse("25", "EUR"))
        payments = RacingPaymentRepository(self.kit.store, winner)
        handler = CreatePaymentHandler(payments, self.kit.permissions, self.kit.ids)

        result = handler.handle(self.create())

        self.assertFalse(result.created)
        self.assertEqual(result.payment_id, PaymentId("payment-winner"))
        self.assertEqual(list(self.kit.store.payments), [PaymentId("payment-winner")])


class RacingPaymentRepository(InMemoryPaymentRepository):
    """Another request stores its Payment between the lookup and the insert."""

    def __init__(self, store: InMemoryPaymentStore, winner: Payment) -> None:
        super().__init__(store)
        self._winner: Payment | None = winner

    def get_by_order(self, order_id: OrderId) -> Payment | None:
        found = super().get_by_order(order_id)
        if self._winner is not None:
            winner, self._winner = self._winner, None
            super().add(winner)
        return found


if __name__ == "__main__":
    unittest.main()
