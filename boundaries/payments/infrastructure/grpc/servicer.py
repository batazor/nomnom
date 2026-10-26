"""grpcio servicer of ``nomnom.payments.v1.PaymentsService``."""

from dataclasses import dataclass

import grpc

from infrastructure.grpc.errors import application_errors
from infrastructure.grpc.generated import payments_pb2, service_pb2, service_pb2_grpc
from infrastructure.grpc.identity import SubjectResolver
from pkg.application import Handler
from slices.payment.applications.authorize_payment import (
    AuthorizePaymentInput,
    AuthorizePaymentResult,
)
from slices.payment.applications.cancel_authorization import (
    CancelAuthorizationInput,
    CancelAuthorizationResult,
)
from slices.payment.applications.capture_payment import (
    CapturePaymentInput,
    CapturePaymentResult,
)
from slices.payment.applications.create_payment import (
    CreatePaymentInput,
    CreatePaymentResult,
)
from slices.payment.applications.get_payment_status import (
    GetPaymentStatusInput,
    GetPaymentStatusResult,
)
from slices.payment.applications.operation_flow import (
    OperationStatus,
    PaymentOperationResult,
)
from slices.payment.applications.refund_payment import (
    RefundPaymentInput,
    RefundPaymentResult,
)
from slices.payment.domains.payment import PaymentState


@dataclass(frozen=True)
class PaymentsHandlers:
    """Application handlers served by ``PaymentsService``; DI wraps them in telemetry."""

    get_payment_status: Handler[GetPaymentStatusInput, GetPaymentStatusResult]
    create_payment: Handler[CreatePaymentInput, CreatePaymentResult]
    authorize_payment: Handler[AuthorizePaymentInput, AuthorizePaymentResult]
    capture_payment: Handler[CapturePaymentInput, CapturePaymentResult]
    cancel_authorization: Handler[CancelAuthorizationInput, CancelAuthorizationResult]
    refund_payment: Handler[RefundPaymentInput, RefundPaymentResult]


class PaymentsService(service_pb2_grpc.PaymentsServiceServicer):  # type: ignore[misc]
    """gRPC servicer: resolves the caller, runs the use case, maps the result to Protobuf.

    Application errors become gRPC status codes; a provider decline is a regular ``DECLINED``
    operation result.
    """

    def __init__(self, handlers: PaymentsHandlers, subjects: SubjectResolver) -> None:
        self._handlers = handlers
        self._subjects = subjects

    def GetPaymentStatus(  # noqa: N802 - name fixed by the generated servicer
        self,
        request: service_pb2.GetPaymentStatusRequest,
        context: grpc.ServicerContext,
    ) -> service_pb2.GetPaymentStatusResponse:
        """Return the current state of a Payment."""
        subject = self._subjects.resolve(context)
        with application_errors(context):
            result = self._handlers.get_payment_status.handle(
                GetPaymentStatusInput(subject=subject, payment_id=request.payment_id)
            )

        return service_pb2.GetPaymentStatusResponse(
            payment=payments_pb2.Payment(
                payment_id=str(result.payment_id),
                order_id=str(result.order_id),
                market_id=str(result.market_id),
                amount=payments_pb2.Money(
                    amount=str(result.amount),
                    currency=result.currency,
                ),
                state=_payment_state(result.state),
            )
        )

    def CreatePayment(  # noqa: N802 - name fixed by the generated servicer
        self,
        request: service_pb2.CreatePaymentRequest,
        context: grpc.ServicerContext,
    ) -> service_pb2.CreatePaymentResponse:
        """Create the Payment of an Order; a repeat returns it with ``created=False``."""
        subject = self._subjects.resolve(context)
        with application_errors(context):
            result = self._handlers.create_payment.handle(
                CreatePaymentInput(
                    subject=subject,
                    order_id=request.order_id,
                    market_id=request.market_id,
                    amount=request.amount.amount,
                    currency=request.amount.currency,
                )
            )
        return service_pb2.CreatePaymentResponse(
            payment_id=str(result.payment_id),
            state=_payment_state(result.state),
            created=result.created,
        )

    def AuthorizePayment(  # noqa: N802 - name fixed by the generated servicer
        self,
        request: service_pb2.AuthorizePaymentRequest,
        context: grpc.ServicerContext,
    ) -> service_pb2.AuthorizePaymentResponse:
        """Run the Authorization Payment Operation under the caller's idempotency key."""
        subject = self._subjects.resolve(context)
        with application_errors(context):
            result = self._handlers.authorize_payment.handle(
                AuthorizePaymentInput(subject, request.payment_id, request.idempotency_key)
            )
        return service_pb2.AuthorizePaymentResponse(operation=_operation(result))

    def CapturePayment(  # noqa: N802 - name fixed by the generated servicer
        self,
        request: service_pb2.CapturePaymentRequest,
        context: grpc.ServicerContext,
    ) -> service_pb2.CapturePaymentResponse:
        """Run the Capture of the authorized amount under the caller's idempotency key."""
        subject = self._subjects.resolve(context)
        with application_errors(context):
            result = self._handlers.capture_payment.handle(
                CapturePaymentInput(subject, request.payment_id, request.idempotency_key)
            )
        return service_pb2.CapturePaymentResponse(operation=_operation(result))

    def CancelAuthorization(  # noqa: N802 - name fixed by the generated servicer
        self,
        request: service_pb2.CancelAuthorizationRequest,
        context: grpc.ServicerContext,
    ) -> service_pb2.CancelAuthorizationResponse:
        """Release the authorized amount without Capture under the caller's idempotency key."""
        subject = self._subjects.resolve(context)
        with application_errors(context):
            result = self._handlers.cancel_authorization.handle(
                CancelAuthorizationInput(subject, request.payment_id, request.idempotency_key)
            )
        return service_pb2.CancelAuthorizationResponse(operation=_operation(result))

    def RefundPayment(  # noqa: N802 - name fixed by the generated servicer
        self,
        request: service_pb2.RefundPaymentRequest,
        context: grpc.ServicerContext,
    ) -> service_pb2.RefundPaymentResponse:
        """Run the full Refund of the captured amount under the caller's idempotency key."""
        subject = self._subjects.resolve(context)
        with application_errors(context):
            result = self._handlers.refund_payment.handle(
                RefundPaymentInput(subject, request.payment_id, request.idempotency_key)
            )
        return service_pb2.RefundPaymentResponse(operation=_operation(result))


def _payment_state(state: PaymentState) -> int:
    return {
        PaymentState.PENDING: payments_pb2.PAYMENT_STATE_CREATED,
        PaymentState.AUTHORIZED: payments_pb2.PAYMENT_STATE_AUTHORIZED,
        PaymentState.CAPTURED: payments_pb2.PAYMENT_STATE_CAPTURED,
        PaymentState.REFUNDED: payments_pb2.PAYMENT_STATE_REFUNDED,
        PaymentState.FAILED: payments_pb2.PAYMENT_STATE_FAILED,
    }[state]


def _operation_status(status: OperationStatus) -> int:
    return {
        OperationStatus.SUCCEEDED: payments_pb2.PAYMENT_OPERATION_STATUS_SUCCEEDED,
        OperationStatus.DECLINED: payments_pb2.PAYMENT_OPERATION_STATUS_DECLINED,
        OperationStatus.PENDING: payments_pb2.PAYMENT_OPERATION_STATUS_PENDING,
    }[status]


def _operation(result: PaymentOperationResult) -> payments_pb2.PaymentOperation:
    """A provider decline is a regular result with ``DECLINED``, never a gRPC error."""
    return payments_pb2.PaymentOperation(
        payment_id=str(result.payment_id),
        operation_id=str(result.operation_id),
        status=_operation_status(result.status),
        payment_state=_payment_state(result.payment_state),
        decline_reason=result.decline_reason or "",
        replayed=result.replayed,
    )
