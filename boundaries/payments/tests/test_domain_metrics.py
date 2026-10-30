"""Business counters of Payment Operations: recorded only after the outer commit."""

import unittest
from collections.abc import Sequence
from typing import cast, override

import pytest
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import InMemoryMetricReader, NumberDataPoint
from payment_fakes import MARKET, ORCHESTRATOR, PaymentsTestKit
from psycopg import errors

from infrastructure.observability import DomainMetrics, ObservedOutbox, ObservedUnitOfWork
from infrastructure.repository import Database
from pkg.outbox import AggregateRef
from slices.payment.applications.authorize_payment import (
    AuthorizePaymentHandler,
    AuthorizePaymentInput,
)
from slices.payment.applications.capture_payment import CapturePaymentHandler, CapturePaymentInput
from slices.payment.applications.operation_flow import (
    PaymentOperationExecutor,
    ProviderResultApplier,
)
from slices.payment.applications.ports import ProviderOperationOutcome
from slices.payment.applications.refund_payment import RefundPaymentHandler, RefundPaymentInput
from slices.payment.domains.payment import (
    Money,
    OperationId,
    OperationKind,
    OrderId,
    PaymentAuthorizationCancelled,
    PaymentAuthorized,
    PaymentDomainEvent,
    PaymentFailed,
    PaymentId,
    PaymentOperationDeclined,
    PaymentState,
)

AUTHORIZATIONS = "nomnom.payments.authorizations"
CAPTURES = "nomnom.payments.captures"
REFUNDS = "nomnom.payments.refunds"
AGGREGATE = AggregateRef("Payment", "payment-0", 2)
AUTHORIZED = PaymentAuthorized(
    PaymentId("payment-0"), OrderId("order-1"), MARKET, Money.parse("1", "EUR")
)


@pytest.mark.usefixtures("database")
class DomainMetricsTest(unittest.TestCase):
    database: Database

    @override
    def setUp(self) -> None:
        self.reader = InMemoryMetricReader()
        provider = MeterProvider(metric_readers=[self.reader])
        self.metrics = DomainMetrics(provider.get_meter("test"))
        self.kit = PaymentsTestKit(self.database)
        self.uow = ObservedUnitOfWork(self.kit.uow, self.metrics)
        self.outbox = ObservedOutbox(self.kit.outbox, self.metrics)

    def counts(self) -> dict[str, dict[str, float]]:
        """Counter name → ``nomnom.outcome`` → value; only series that exist."""
        result: dict[str, dict[str, float]] = {}
        data = self.reader.get_metrics_data()
        if data is None:
            return result
        for rm in data.resource_metrics:
            for sm in rm.scope_metrics:
                for metric in sm.metrics:
                    for point in metric.data.data_points:
                        assert isinstance(point, NumberDataPoint)
                        attributes = dict(point.attributes or {})
                        self.assertEqual(set(attributes), {"nomnom.outcome"})
                        outcome = str(attributes["nomnom.outcome"])
                        result.setdefault(metric.name, {})[outcome] = point.value
        return result

    def executor(self) -> PaymentOperationExecutor:
        return PaymentOperationExecutor(
            uow=self.uow,
            payments=self.kit.payments,
            operations=self.kit.operations,
            pending=self.kit.pending,
            provider=self.kit.provider,
            permissions=self.kit.permissions,
            clock=self.kit.clock,
            operation_ids=self.kit.ids,
            applier=ProviderResultApplier(
                self.kit.payments,
                self.kit.operations,
                self.outbox,
                self.kit.clock,
                self.kit.transitions,
            ),
            transitions=self.kit.transitions,
        )

    def test_commit_records_events(self) -> None:
        self.uow.run(lambda: self.outbox.add(AGGREGATE, [AUTHORIZED]))

        self.assertEqual(self.counts(), {AUTHORIZATIONS: {"authorized": 1}})

    def test_nothing_recorded_before_commit(self) -> None:
        def work() -> None:
            self.outbox.add(AGGREGATE, [AUTHORIZED])
            self.assertEqual(self.counts(), {})

        self.uow.run(work)

        self.assertEqual(self.counts(), {AUTHORIZATIONS: {"authorized": 1}})

    def test_rollback_records_nothing(self) -> None:
        def work() -> None:
            self.outbox.add(AGGREGATE, [AUTHORIZED])
            raise RuntimeError("boom")

        with self.assertRaises(RuntimeError):
            self.uow.run(work)

        self.assertEqual(self.kit.uow.rollbacks, 1)
        self.assertEqual(self.counts(), {})

    def test_failed_outbox_write_records_nothing(self) -> None:
        self.kit.outbox.fail_next = True

        with self.assertRaises(RuntimeError):
            self.uow.run(lambda: self.outbox.add(AGGREGATE, [AUTHORIZED]))

        self.assertEqual(self.counts(), {})

    def test_nested_unit_of_work_records_once_at_outer_commit(self) -> None:
        def outer() -> None:
            self.uow.run(lambda: self.outbox.add(AGGREGATE, [AUTHORIZED]))
            self.assertEqual(self.counts(), {})

        self.uow.run(outer)

        self.assertEqual(self.kit.uow.commits, 1)
        self.assertEqual(self.counts(), {AUTHORIZATIONS: {"authorized": 1}})

    def test_inner_rollback_rolls_back_outer_records_nothing(self) -> None:
        def inner() -> None:
            self.outbox.add(AGGREGATE, [AUTHORIZED])
            raise RuntimeError("boom")

        with self.assertRaises(RuntimeError):
            self.uow.run(lambda: self.uow.run(inner))

        self.assertEqual(self.counts(), {})

    def test_retried_attempt_counts_once(self) -> None:
        attempts: list[int] = []

        def work() -> None:
            attempts.append(len(attempts) + 1)
            self.outbox.add(AGGREGATE, [AUTHORIZED])
            if len(attempts) == 1:
                raise errors.DeadlockDetected("deadlock detected")

        self.uow.run(work)

        # События откатанной попытки не считаются: только попытка, которая закоммитилась.
        self.assertEqual(attempts, [1, 2])
        self.assertEqual(self.counts(), {AUTHORIZATIONS: {"authorized": 1}})

    def test_unknown_values_create_no_series(self) -> None:
        payment, order = PaymentId("payment-0"), OrderId("order-1")
        events: Sequence[PaymentDomainEvent] = [
            PaymentFailed(payment, order, MARKET, "card_declined"),
            PaymentAuthorizationCancelled(payment, order, MARKET, Money.parse("1", "EUR")),
            PaymentOperationDeclined(
                OperationId("op-1"), payment, OperationKind.CANCEL_AUTHORIZATION, "x"
            ),
            PaymentOperationDeclined(
                OperationId("op-2"),
                payment,
                cast(OperationKind, "x"),  # ty: ignore[disjoint-cast] - deliberately unknown kind
                "x",
            ),
            cast(PaymentFailed, object()),
        ]

        self.uow.run(lambda: self.outbox.add(AGGREGATE, events))

        self.assertEqual(self.counts(), {})

    def test_declined_authorization_counted_as_declined(self) -> None:
        payment_id = self.kit.given_payment()
        self.kit.provider.respond(
            OperationKind.AUTHORIZE, ProviderOperationOutcome.declined("card_declined")
        )

        AuthorizePaymentHandler(self.executor()).handle(
            AuthorizePaymentInput(ORCHESTRATOR, payment_id, "order-1:authorize")
        )

        self.assertEqual(self.kit.payment_state(payment_id), PaymentState.FAILED)
        self.assertEqual(self.counts(), {AUTHORIZATIONS: {"declined": 1}})

    def test_idempotent_replay_counts_once(self) -> None:
        payment_id = self.kit.given_payment()
        handler = AuthorizePaymentHandler(self.executor())
        request = AuthorizePaymentInput(ORCHESTRATOR, payment_id, "order-1:authorize")

        handler.handle(request)
        replay = handler.handle(request)

        self.assertTrue(replay.replayed)
        self.assertEqual(self.counts(), {AUTHORIZATIONS: {"authorized": 1}})

    def test_capture_and_refund_outcomes(self) -> None:
        captured = self.kit.given_payment(PaymentState.AUTHORIZED, "payment-1", "order-1")
        refunded = self.kit.given_payment(PaymentState.CAPTURED, "payment-2", "order-2")
        declined = self.kit.given_payment(PaymentState.AUTHORIZED, "payment-3", "order-3")
        refund_declined = self.kit.given_payment(PaymentState.CAPTURED, "payment-4", "order-4")
        executor = self.executor()

        CapturePaymentHandler(executor).handle(CapturePaymentInput(ORCHESTRATOR, captured, "c-1"))
        RefundPaymentHandler(executor).handle(RefundPaymentInput(ORCHESTRATOR, refunded, "r-1"))
        self.kit.provider.respond(
            OperationKind.CAPTURE, ProviderOperationOutcome.declined("insufficient_funds")
        )
        CapturePaymentHandler(executor).handle(CapturePaymentInput(ORCHESTRATOR, declined, "c-2"))
        self.kit.provider.respond(
            OperationKind.REFUND, ProviderOperationOutcome.declined("refund_window_closed")
        )
        RefundPaymentHandler(executor).handle(
            RefundPaymentInput(ORCHESTRATOR, refund_declined, "r-2")
        )

        # A provider decline is the business result declined, not a technical failure.
        self.assertEqual(
            self.counts(),
            {
                CAPTURES: {"captured": 1, "declined": 1},
                REFUNDS: {"refunded": 1, "declined": 1},
            },
        )


if __name__ == "__main__":
    unittest.main()
