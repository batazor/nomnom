"""``nomnom.fsm.transitions`` of the Payment state machine (ops/semconv fsm.yaml)."""

import unittest
from typing import override

import pytest
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import InMemoryMetricReader, NumberDataPoint
from payment_fakes import ORCHESTRATOR, PaymentsTestKit

from infrastructure.observability import FsmMetrics, ObservedUnitOfWork
from infrastructure.repository import Database
from slices.payment.applications.authorize_payment import (
    AuthorizePaymentHandler,
    AuthorizePaymentInput,
)
from slices.payment.applications.capture_payment import CapturePaymentHandler, CapturePaymentInput
from slices.payment.applications.errors import PaymentStateConflictError
from slices.payment.applications.operation_flow import (
    PaymentOperationExecutor,
    ProviderResultApplier,
)
from slices.payment.applications.ports import ProviderOperationOutcome
from slices.payment.applications.refund_payment import RefundPaymentHandler, RefundPaymentInput
from slices.payment.domains.payment import (
    InvalidTransitionError,
    OperationKind,
    PaymentState,
    PaymentTrigger,
    TransitionResult,
)

# (from_state, trigger, to_state, result, terminal) → count
Transitions = dict[tuple[str, str, str, str, bool], float]


@pytest.mark.usefixtures("database")
class FsmMetricsTest(unittest.TestCase):
    database: Database

    @override
    def setUp(self) -> None:
        self.reader = InMemoryMetricReader()
        provider = MeterProvider(metric_readers=[self.reader])
        self.kit = PaymentsTestKit(self.database)
        self.fsm = FsmMetrics(provider.get_meter("test"))
        self.uow = ObservedUnitOfWork(self.kit.uow, self.fsm)

    def transitions(self) -> Transitions:
        result: Transitions = {}
        data = self.reader.get_metrics_data()
        if data is None:
            return result
        for rm in data.resource_metrics:
            for sm in rm.scope_metrics:
                for metric in sm.metrics:
                    self.assertEqual(metric.name, "nomnom.fsm.transitions")
                    for point in metric.data.data_points:
                        assert isinstance(point, NumberDataPoint)
                        attributes = dict(point.attributes or {})
                        self.assertEqual(attributes.pop("nomnom.fsm.aggregate_type"), "payment")
                        key = (
                            str(attributes.pop("nomnom.fsm.from_state")),
                            str(attributes.pop("nomnom.fsm.trigger")),
                            str(attributes.pop("nomnom.fsm.to_state")),
                            str(attributes.pop("nomnom.fsm.result")),
                            bool(attributes.pop("nomnom.fsm.terminal")),
                        )
                        self.assertEqual(attributes, {})
                        result[key] = point.value
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
                self.kit.payments, self.kit.operations, self.kit.outbox, self.kit.clock, self.fsm
            ),
            transitions=self.fsm,
        )

    def test_applied_transitions_count_after_commit(self) -> None:
        authorized = self.kit.given_payment(payment_id="payment-1", order_id="order-1")
        refunded = self.kit.given_payment(PaymentState.CAPTURED, "payment-2", "order-2")
        executor = self.executor()

        AuthorizePaymentHandler(executor).handle(
            AuthorizePaymentInput(ORCHESTRATOR, authorized, "a-1")
        )
        RefundPaymentHandler(executor).handle(RefundPaymentInput(ORCHESTRATOR, refunded, "r-1"))

        self.assertEqual(
            self.transitions(),
            {
                ("pending", "authorize", "authorized", "applied", False): 1,
                ("captured", "refund", "refunded", "applied", True): 1,
            },
        )

    def test_declined_authorization_fails_the_payment(self) -> None:
        payment_id = self.kit.given_payment()
        self.kit.provider.respond(
            OperationKind.AUTHORIZE, ProviderOperationOutcome.declined("card_declined")
        )

        AuthorizePaymentHandler(self.executor()).handle(
            AuthorizePaymentInput(ORCHESTRATOR, payment_id, "a-1")
        )

        self.assertEqual(self.transitions(), {("pending", "fail", "failed", "applied", True): 1})

    def test_invalid_trigger_is_rejected_and_keeps_the_state(self) -> None:
        payment_id = self.kit.given_payment()

        with self.assertRaises(PaymentStateConflictError):
            CapturePaymentHandler(self.executor()).handle(
                CapturePaymentInput(ORCHESTRATOR, payment_id, "c-1")
            )

        self.assertEqual(
            self.transitions(), {("pending", "capture", "pending", "rejected", False): 1}
        )

    def test_repeated_outcome_is_not_a_transition(self) -> None:
        payment_id = self.kit.given_payment(PaymentState.AUTHORIZED)

        with self.assertRaises(PaymentStateConflictError):
            AuthorizePaymentHandler(self.executor()).handle(
                AuthorizePaymentInput(ORCHESTRATOR, payment_id, "a-1")
            )

        self.assertEqual(self.transitions(), {})

    def test_rollback_records_no_applied_transition(self) -> None:
        transition = TransitionResult(
            PaymentState.PENDING, PaymentTrigger.AUTHORIZE, PaymentState.AUTHORIZED
        )

        def work() -> None:
            self.fsm.applied([transition])
            self.assertEqual(self.transitions(), {})
            raise RuntimeError("boom")

        with self.assertRaises(RuntimeError):
            self.uow.run(work)

        self.assertEqual(self.transitions(), {})

    def test_rejection_inside_a_rolled_back_unit_of_work_still_counts(self) -> None:
        def work() -> None:
            self.fsm.rejected(InvalidTransitionError(PaymentState.REFUNDED, PaymentTrigger.FAIL))
            raise RuntimeError("boom")

        with self.assertRaises(RuntimeError):
            self.uow.run(work)

        self.assertEqual(
            self.transitions(), {("refunded", "fail", "refunded", "rejected", True): 1}
        )


if __name__ == "__main__":
    unittest.main()
