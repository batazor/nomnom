"""Mapper of payments Domain Events to nomnom.payments.v1 Integration Events (ADR 31)."""

import unittest
import uuid
from typing import Any, override

from infrastructure.messaging import PaymentEventsMapper
from infrastructure.messaging.events import events_pb2
from infrastructure.postgres import EVENT_ID_NAMESPACE, PAYMENT_EVENTS_TOPIC, outbox_event_id
from pkg.application import AggregateRef
from slices.payment.domains.payment import (
    Money,
    OperationId,
    OperationKind,
    OrderId,
    PaymentAuthorizationCancelled,
    PaymentAuthorized,
    PaymentCaptured,
    PaymentFailed,
    PaymentId,
    PaymentOperationDeclined,
    PaymentRefunded,
)

AGGREGATE = AggregateRef("Payment", "payment-1", 3)
PAYMENT = PaymentId("payment-1")
ORDER = OrderId("order-1")
AMOUNT = Money.parse("25.50", "EUR")


class PaymentEventsMapperTest(unittest.TestCase):
    @override
    def setUp(self) -> None:
        self.mapper = PaymentEventsMapper()

    def test_amount_events_map_to_their_contracts(self) -> None:
        cases = [
            (PaymentAuthorized(PAYMENT, ORDER, AMOUNT), events_pb2.PaymentAuthorized),
            (PaymentCaptured(PAYMENT, ORDER, AMOUNT), events_pb2.PaymentCaptured),
            (
                PaymentAuthorizationCancelled(PAYMENT, ORDER, AMOUNT),
                events_pb2.PaymentAuthorizationCancelled,
            ),
            (PaymentRefunded(PAYMENT, ORDER, AMOUNT), events_pb2.PaymentRefunded),
        ]
        for event, contract in cases:
            with self.subTest(contract=contract.DESCRIPTOR.full_name):
                message = self.mapper.map(AGGREGATE, event)
                assert message is not None
                self.assertEqual(message.topic, PAYMENT_EVENTS_TOPIC)
                self.assertEqual(
                    message.event_type, f"nomnom.payments.v1.{contract.DESCRIPTOR.name}"
                )
                decoded = contract.FromString(message.payload)
                self.assertEqual(decoded.payment_id, "payment-1")
                self.assertEqual(decoded.order_id, "order-1")
                self.assertEqual(decoded.amount.amount, "25.50")
                self.assertEqual(decoded.amount.currency, "EUR")

    def test_failed_carries_reason(self) -> None:
        message = self.mapper.map(AGGREGATE, PaymentFailed(PAYMENT, ORDER, "card_declined"))
        assert message is not None
        self.assertEqual(message.event_type, "nomnom.payments.v1.PaymentFailed")
        decoded = events_pb2.PaymentFailed.FromString(message.payload)
        self.assertEqual(
            (decoded.payment_id, decoded.order_id, decoded.reason),
            ("payment-1", "order-1", "card_declined"),
        )

    def test_operation_declined_is_not_published(self) -> None:
        event = PaymentOperationDeclined(
            OperationId("op-1"), PAYMENT, OperationKind.CAPTURE, "insufficient_funds"
        )
        self.assertIsNone(self.mapper.map(AGGREGATE, event))

    def test_unknown_event_is_not_published(self) -> None:
        unknown: Any = object()
        self.assertIsNone(self.mapper.map(AGGREGATE, unknown))


class OutboxEventIdTest(unittest.TestCase):
    def test_uuid_v5_of_adr_31_namespace_and_name(self) -> None:
        self.assertEqual(EVENT_ID_NAMESPACE, uuid.UUID("8f1d3c2a-5b7e-4a90-9c61-2e4f7b8d0a13"))
        event_id = outbox_event_id(AGGREGATE, "nomnom.payments.v1.PaymentAuthorized")
        self.assertEqual(event_id.version, 5)
        self.assertEqual(
            event_id,
            uuid.uuid5(
                uuid.UUID("8f1d3c2a-5b7e-4a90-9c61-2e4f7b8d0a13"),
                "Payment:payment-1:3:nomnom.payments.v1.PaymentAuthorized",
            ),
        )

    def test_differs_by_version_and_type(self) -> None:
        base = outbox_event_id(AGGREGATE, "nomnom.payments.v1.PaymentAuthorized")
        self.assertEqual(base, outbox_event_id(AGGREGATE, "nomnom.payments.v1.PaymentAuthorized"))
        self.assertNotEqual(
            base,
            outbox_event_id(
                AggregateRef("Payment", "payment-1", 4), "nomnom.payments.v1.PaymentAuthorized"
            ),
        )
        self.assertNotEqual(base, outbox_event_id(AGGREGATE, "nomnom.payments.v1.PaymentCaptured"))


if __name__ == "__main__":
    unittest.main()
