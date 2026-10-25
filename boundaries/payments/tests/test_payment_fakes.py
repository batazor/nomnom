import unittest
from typing import override

from payment_fakes import PaymentsTestKit

from pkg.application import AggregateRef, ConcurrencyConflictError
from slices.payment.applications.ports import ProviderOperationRequest, ProviderUnavailableError
from slices.payment.domains.payment import (
    DuplicateIdempotencyKeyError,
    DuplicatePaymentError,
    Money,
    OperationAlreadyPendingError,
    OperationId,
    OperationKind,
    OrderId,
    Payment,
    PaymentFailed,
    PaymentId,
    PaymentOperation,
)


class InMemoryAdaptersTest(unittest.TestCase):
    @override
    def setUp(self) -> None:
        self.kit = PaymentsTestKit()

    def test_unit_of_work_rolls_back_every_change(self) -> None:
        payment_id = self.kit.given_payment()

        def work() -> None:
            payment = self.kit.payments.get(payment_id)
            assert payment is not None
            payment.fail("declined")
            self.kit.payments.save(payment)
            self.kit.inbox.register("message-1")
            self.kit.outbox.add(
                AggregateRef("Payment", payment_id, 2),
                [PaymentFailed(payment_id, OrderId("order-1"), "declined")],
            )
            raise RuntimeError("boom")

        with self.assertRaises(RuntimeError):
            self.kit.uow.run(work)

        self.assertEqual(self.kit.store.payments[payment_id].version, 1)
        self.assertEqual(self.kit.published(), [])
        self.assertTrue(self.kit.inbox.register("message-1"))
        self.assertEqual((self.kit.uow.commits, self.kit.uow.rollbacks), (0, 1))

    def test_nested_unit_of_work_joins_outer_transaction(self) -> None:
        result = self.kit.uow.run(lambda: self.kit.uow.run(lambda: 42))

        self.assertEqual(result, 42)
        self.assertEqual(self.kit.uow.commits, 1)

    def test_payment_repository_enforces_order_uniqueness_and_version(self) -> None:
        amount = Money.parse("5", "EUR")
        self.kit.payments.add(Payment(PaymentId("p-1"), OrderId("o-1"), amount))
        with self.assertRaises(DuplicatePaymentError):
            self.kit.payments.add(Payment(PaymentId("p-2"), OrderId("o-1"), amount))

        first = self.kit.payments.get(PaymentId("p-1"))
        second = self.kit.payments.get(PaymentId("p-1"))
        assert first is not None and second is not None
        self.assertEqual(self.kit.payments.save(first), 2)
        with self.assertRaises(ConcurrencyConflictError):
            self.kit.payments.save(second)

    def test_operation_repository_enforces_key_and_single_pending(self) -> None:
        payment_id = self.kit.given_payment()
        now = self.kit.clock.now()
        amount = Money.parse("25.00", "EUR")

        def operation(operation_id: str, key: str) -> PaymentOperation:
            return PaymentOperation(
                OperationId(operation_id), payment_id, OperationKind.AUTHORIZE, key, amount, now
            )

        self.kit.operations.add(operation("op-1", "key-1"))
        with self.assertRaises(DuplicateIdempotencyKeyError):
            self.kit.operations.add(operation("op-2", "key-1"))
        with self.assertRaises(OperationAlreadyPendingError):
            self.kit.operations.add(operation("op-3", "key-2"))

    def test_fake_provider_deduplicates_by_idempotency_key(self) -> None:
        request = ProviderOperationRequest(
            OperationId("op-1"),
            PaymentId("p-1"),
            OperationKind.CAPTURE,
            "key-1",
            Money.parse("1", "EUR"),
            "auth-ref",
        )
        self.kit.provider.lost_responses = 1

        with self.assertRaises(ProviderUnavailableError):
            self.kit.provider.execute(request)
        outcome = self.kit.provider.execute(request)

        self.assertEqual(outcome.provider_reference, "capture-ref-0")
        self.assertEqual(self.kit.provider.effect_count(OperationKind.CAPTURE), 1)


if __name__ == "__main__":
    unittest.main()
