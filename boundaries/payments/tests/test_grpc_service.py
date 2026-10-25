import time
import unittest
from typing import cast

import grpc
from dependency_injector import providers
from payment_fakes import PaymentsTestKit, close_container, payments_container

from infrastructure.authorization import DenyAllPermissions
from infrastructure.grpc.service import (
    GrpcWorker,
    PaymentsHandlers,
    SubjectResolver,
    payments_pb2,
    service_pb2,
    service_pb2_grpc,
)
from pkg.application import Subject, SubjectKind
from slices.payment.applications.authorize_payment import AuthorizePaymentHandler
from slices.payment.applications.cancel_authorization import CancelAuthorizationHandler
from slices.payment.applications.capture_payment import CapturePaymentHandler
from slices.payment.applications.create_payment import CreatePaymentHandler
from slices.payment.applications.get_payment_status import GetPaymentStatusHandler
from slices.payment.applications.ports import PaymentPermission, ProviderOperationOutcome
from slices.payment.applications.refund_payment import RefundPaymentHandler
from slices.payment.domains.payment import OperationKind, PaymentState

ORDERS_WORKER = Subject(SubjectKind.SERVICE, "orders-worker")


class StaticSubjectResolver(SubjectResolver):
    def resolve(self, context: grpc.ServicerContext) -> Subject:
        return ORDERS_WORKER


def _handlers(kit: PaymentsTestKit) -> PaymentsHandlers:
    executor = kit.executor()
    return PaymentsHandlers(
        get_payment_status=GetPaymentStatusHandler(kit.payments, kit.permissions),
        create_payment=CreatePaymentHandler(kit.payments, kit.permissions, kit.ids),
        authorize_payment=AuthorizePaymentHandler(executor),
        capture_payment=CapturePaymentHandler(executor),
        cancel_authorization=CancelAuthorizationHandler(executor),
        refund_payment=RefundPaymentHandler(executor),
    )


def _code(error: grpc.RpcError) -> grpc.StatusCode:
    return cast(grpc.Call, error).code()


class PaymentsGrpcServiceTest(unittest.TestCase):
    def setUp(self) -> None:
        self.kit = PaymentsTestKit()
        self.worker = GrpcWorker(("127.0.0.1", 0), _handlers(self.kit), StaticSubjectResolver())
        self.worker.start()
        self.channel = grpc.insecure_channel(f"127.0.0.1:{self.worker.port}")
        self.client = service_pb2_grpc.PaymentsServiceStub(self.channel)

    def tearDown(self) -> None:
        self.channel.close()
        self.worker.shutdown(time.monotonic() + 1)

    def _create(self, order_id: str = "order-1", amount: str = "25.00") -> str:
        response = self.client.CreatePayment(
            service_pb2.CreatePaymentRequest(
                order_id=order_id, amount=payments_pb2.Money(amount=amount, currency="EUR")
            )
        )
        return response.payment_id

    def _authorize(
        self, payment_id: str, key: str = "order-1:authorize"
    ) -> payments_pb2.PaymentOperation:
        return self.client.AuthorizePayment(
            service_pb2.AuthorizePaymentRequest(payment_id=payment_id, idempotency_key=key)
        ).operation

    def test_get_payment_status_maps_the_application_view(self) -> None:
        self.kit.given_payment(PaymentState.AUTHORIZED, payment_id="payment-1")

        response = self.client.GetPaymentStatus(
            service_pb2.GetPaymentStatusRequest(payment_id="payment-1")
        )

        self.assertEqual(response.payment.payment_id, "payment-1")
        self.assertEqual(response.payment.order_id, "order-1")
        self.assertEqual(response.payment.amount.amount, "25.00")
        self.assertEqual(response.payment.amount.currency, "EUR")
        self.assertEqual(response.payment.state, payments_pb2.PAYMENT_STATE_AUTHORIZED)

    def test_missing_payment_is_not_found(self) -> None:
        with self.assertRaises(grpc.RpcError) as raised:
            self.client.GetPaymentStatus(service_pb2.GetPaymentStatusRequest(payment_id="missing"))
        self.assertEqual(_code(raised.exception), grpc.StatusCode.NOT_FOUND)

    def test_create_then_authorize_succeeds(self) -> None:
        created = self.client.CreatePayment(
            service_pb2.CreatePaymentRequest(
                order_id="order-1", amount=payments_pb2.Money(amount="25.00", currency="EUR")
            )
        )
        self.assertTrue(created.created)
        self.assertEqual(created.state, payments_pb2.PAYMENT_STATE_CREATED)

        operation = self._authorize(created.payment_id)

        self.assertEqual(operation.payment_id, created.payment_id)
        self.assertTrue(operation.operation_id)
        self.assertEqual(operation.status, payments_pb2.PAYMENT_OPERATION_STATUS_SUCCEEDED)
        self.assertEqual(operation.payment_state, payments_pb2.PAYMENT_STATE_AUTHORIZED)
        self.assertEqual(operation.decline_reason, "")
        self.assertFalse(operation.replayed)
        checked = {(subject, permission) for subject, permission, _ in self.kit.permissions.checks}
        self.assertIn((ORDERS_WORKER, PaymentPermission.CREATE), checked)
        self.assertIn((ORDERS_WORKER, PaymentPermission.AUTHORIZE), checked)

    def test_create_payment_is_idempotent_by_order(self) -> None:
        first = self._create()
        repeated = self.client.CreatePayment(
            service_pb2.CreatePaymentRequest(
                order_id="order-1", amount=payments_pb2.Money(amount="25.00", currency="EUR")
            )
        )

        self.assertEqual(repeated.payment_id, first)
        self.assertFalse(repeated.created)

    def test_create_payment_with_other_terms_already_exists(self) -> None:
        self._create(amount="25.00")
        with self.assertRaises(grpc.RpcError) as raised:
            self._create(amount="30.00")
        self.assertEqual(_code(raised.exception), grpc.StatusCode.ALREADY_EXISTS)

    def test_repeated_authorize_with_same_key_is_replayed(self) -> None:
        payment_id = self._create()
        first = self._authorize(payment_id)

        repeated = self._authorize(payment_id)

        self.assertTrue(repeated.replayed)
        self.assertEqual(repeated.operation_id, first.operation_id)
        self.assertEqual(repeated.status, payments_pb2.PAYMENT_OPERATION_STATUS_SUCCEEDED)
        self.assertEqual(self.kit.provider.effect_count(OperationKind.AUTHORIZE), 1)

    def test_provider_decline_is_a_declined_result_not_an_error(self) -> None:
        self.kit.provider.respond(
            OperationKind.AUTHORIZE, ProviderOperationOutcome.declined("insufficient funds")
        )
        payment_id = self._create()

        operation = self._authorize(payment_id)

        self.assertEqual(operation.status, payments_pb2.PAYMENT_OPERATION_STATUS_DECLINED)
        self.assertEqual(operation.decline_reason, "insufficient funds")
        self.assertEqual(operation.payment_state, payments_pb2.PAYMENT_STATE_FAILED)

    def test_capture_cancel_and_refund_map_operations(self) -> None:
        captured_id = self._create("order-1")
        self._authorize(captured_id, "order-1:authorize")
        capture = self.client.CapturePayment(
            service_pb2.CapturePaymentRequest(
                payment_id=captured_id, idempotency_key="order-1:capture"
            )
        ).operation
        refund = self.client.RefundPayment(
            service_pb2.RefundPaymentRequest(
                payment_id=captured_id, idempotency_key="order-1:refund"
            )
        ).operation

        cancelled_id = self._create("order-2")
        self._authorize(cancelled_id, "order-2:authorize")
        cancel = self.client.CancelAuthorization(
            service_pb2.CancelAuthorizationRequest(
                payment_id=cancelled_id, idempotency_key="order-2:cancel"
            )
        ).operation

        self.assertEqual(capture.payment_state, payments_pb2.PAYMENT_STATE_CAPTURED)
        self.assertEqual(refund.payment_state, payments_pb2.PAYMENT_STATE_REFUNDED)
        for operation in (capture, refund, cancel):
            self.assertEqual(operation.status, payments_pb2.PAYMENT_OPERATION_STATUS_SUCCEEDED)

    def test_pending_provider_result_is_pending(self) -> None:
        self.kit.provider.respond(OperationKind.AUTHORIZE, ProviderOperationOutcome.accepted())
        payment_id = self._create()

        operation = self._authorize(payment_id)

        self.assertEqual(operation.status, payments_pb2.PAYMENT_OPERATION_STATUS_PENDING)
        self.assertEqual(operation.payment_state, payments_pb2.PAYMENT_STATE_CREATED)

    def test_invalid_create_payment_is_invalid_argument(self) -> None:
        for order_id, amount in (("", "25.00"), ("order-1", "-1"), ("order-1", "abc")):
            with self.subTest(order_id=order_id, amount=amount):
                with self.assertRaises(grpc.RpcError) as raised:
                    self._create(order_id, amount)
                self.assertEqual(_code(raised.exception), grpc.StatusCode.INVALID_ARGUMENT)

    def test_operation_without_idempotency_key_is_invalid_argument(self) -> None:
        payment_id = self._create()
        with self.assertRaises(grpc.RpcError) as raised:
            self._authorize(payment_id, key="")
        self.assertEqual(_code(raised.exception), grpc.StatusCode.INVALID_ARGUMENT)

    def test_operation_on_missing_payment_is_not_found(self) -> None:
        with self.assertRaises(grpc.RpcError) as raised:
            self._authorize("missing")
        self.assertEqual(_code(raised.exception), grpc.StatusCode.NOT_FOUND)

    def test_operation_in_wrong_state_is_failed_precondition(self) -> None:
        payment_id = self._create()
        with self.assertRaises(grpc.RpcError) as raised:
            self.client.CapturePayment(
                service_pb2.CapturePaymentRequest(payment_id=payment_id, idempotency_key="k")
            )
        self.assertEqual(_code(raised.exception), grpc.StatusCode.FAILED_PRECONDITION)

    def test_denied_permission_is_permission_denied(self) -> None:
        self.kit.permissions.denied.add(PaymentPermission.CREATE)
        with self.assertRaises(grpc.RpcError) as raised:
            self._create()
        self.assertEqual(_code(raised.exception), grpc.StatusCode.PERMISSION_DENIED)

    def test_unavailable_provider_is_unavailable(self) -> None:
        payment_id = self._create()
        self.kit.provider.unavailable_times = 1
        with self.assertRaises(grpc.RpcError) as raised:
            self._authorize(payment_id)
        self.assertEqual(_code(raised.exception), grpc.StatusCode.UNAVAILABLE)


class DefaultWorkerTest(unittest.TestCase):
    def test_default_worker_rejects_calls_without_verified_identity(self) -> None:
        container = payments_container()
        self.addCleanup(close_container, container)
        container.permissions.override(providers.Object(DenyAllPermissions()))
        worker = container.grpc_worker()
        worker.start()
        channel = grpc.insecure_channel(f"127.0.0.1:{worker.port}")
        try:
            client = service_pb2_grpc.PaymentsServiceStub(channel)
            with self.assertRaises(grpc.RpcError) as raised:
                client.CreatePayment(
                    service_pb2.CreatePaymentRequest(
                        order_id="order-1",
                        amount=payments_pb2.Money(amount="25.00", currency="EUR"),
                    )
                )
            self.assertEqual(_code(raised.exception), grpc.StatusCode.UNAUTHENTICATED)
        finally:
            channel.close()
            worker.shutdown(time.monotonic() + 1)


if __name__ == "__main__":
    unittest.main()
