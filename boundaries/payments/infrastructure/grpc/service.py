"""grpcio adapter for ``nomnom.payments.v1.PaymentsService``."""

import sys
import time
import uuid
from collections.abc import Callable, Iterator
from concurrent import futures
from contextlib import contextmanager
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import NoReturn, TypeVar

import grpc

from infrastructure.authorization import create_permission_checker
from infrastructure.grpc.identity import (
    RejectingSubjectResolver,
    SubjectResolver,
    create_subject_resolver,
)
from pkg.application import (
    AuthorizationUnavailable,
    ConcurrencyConflict,
    PermissionDenied,
)
from pkg.config import IdentitySettings, SpiceDBSettings
from pkg.lifecycle import DrainTimeoutError, LifecycleError
from slices.payment.applications.authorize_payment import (
    AuthorizePaymentHandler,
    AuthorizePaymentInput,
)
from slices.payment.applications.cancel_authorization import (
    CancelAuthorizationHandler,
    CancelAuthorizationInput,
)
from slices.payment.applications.capture_payment import (
    CapturePaymentHandler,
    CapturePaymentInput,
)
from slices.payment.applications.create_payment import (
    CreatePaymentHandler,
    CreatePaymentInput,
)
from slices.payment.applications.errors import (
    IdempotencyKeyConflict,
    InvalidPaymentRequest,
    OperationInProgress,
    PaymentAlreadyExists,
    PaymentNotFound,
    PaymentOperationNotFound,
    PaymentStateConflict,
    ProviderResultConflict,
)
from slices.payment.applications.get_payment_status import (
    GetPaymentStatusHandler,
    GetPaymentStatusInput,
)
from slices.payment.applications.operation_flow import (
    OperationStatus,
    PaymentOperationExecutor,
    PaymentOperationResult,
)
from slices.payment.applications.ports import PaymentPermissionChecker, ProviderUnavailable
from slices.payment.applications.refund_payment import (
    RefundPaymentHandler,
    RefundPaymentInput,
)
from slices.payment.domains.payment import OperationId, PaymentId, PaymentState

# Generated grpcio code uses absolute ``nomnom.payments.v1`` imports. Keep its
# root private to the transport package instead of leaking it into application code.
_GEN_ROOT = Path(__file__).with_name("gen")
if str(_GEN_ROOT) not in sys.path:
    sys.path.insert(0, str(_GEN_ROOT))

from nomnom.payments.v1 import payments_pb2, service_pb2, service_pb2_grpc  # noqa: E402

T = TypeVar("T")


@dataclass(frozen=True)
class PaymentsHandlers:
    """Application handlers served by ``PaymentsService``."""

    get_payment_status: GetPaymentStatusHandler
    create_payment: CreatePaymentHandler
    authorize_payment: AuthorizePaymentHandler
    capture_payment: CapturePaymentHandler
    cancel_authorization: CancelAuthorizationHandler
    refund_payment: RefundPaymentHandler


# Ordered: the first matching application error decides the gRPC status code.
_ERROR_CODES: tuple[tuple[type[Exception], grpc.StatusCode], ...] = (
    (InvalidPaymentRequest, grpc.StatusCode.INVALID_ARGUMENT),
    (PaymentNotFound, grpc.StatusCode.NOT_FOUND),
    (PaymentOperationNotFound, grpc.StatusCode.NOT_FOUND),
    (PermissionDenied, grpc.StatusCode.PERMISSION_DENIED),
    (AuthorizationUnavailable, grpc.StatusCode.UNAVAILABLE),
    (ProviderUnavailable, grpc.StatusCode.UNAVAILABLE),
    (PaymentAlreadyExists, grpc.StatusCode.ALREADY_EXISTS),
    (PaymentStateConflict, grpc.StatusCode.FAILED_PRECONDITION),
    (IdempotencyKeyConflict, grpc.StatusCode.FAILED_PRECONDITION),
    (OperationInProgress, grpc.StatusCode.ABORTED),
    (ConcurrencyConflict, grpc.StatusCode.ABORTED),
    (ProviderResultConflict, grpc.StatusCode.ABORTED),
)


@contextmanager
def _application_errors(context: grpc.ServicerContext) -> Iterator[None]:
    """Translate application errors into gRPC status codes.

    Subjects are resolved before entering this block: ``context.abort`` raises a plain
    ``Exception`` that must not be remapped to ``INTERNAL``.
    """
    try:
        yield
    except Exception as error:
        for error_type, code in _ERROR_CODES:
            if isinstance(error, error_type):
                _abort(context, code, str(error))
        _abort(context, grpc.StatusCode.INTERNAL, "internal error")


class PaymentsService(service_pb2_grpc.PaymentsServiceServicer):  # type: ignore[misc]
    def __init__(self, handlers: PaymentsHandlers, subjects: SubjectResolver) -> None:
        self._handlers = handlers
        self._subjects = subjects

    def GetPaymentStatus(  # noqa: N802
        self,
        request: service_pb2.GetPaymentStatusRequest,
        context: grpc.ServicerContext,
    ) -> service_pb2.GetPaymentStatusResponse:
        subject = self._subjects.resolve(context)
        with _application_errors(context):
            result = self._handlers.get_payment_status.handle(
                GetPaymentStatusInput(subject=subject, payment_id=request.payment_id)
            )

        return service_pb2.GetPaymentStatusResponse(
            payment=payments_pb2.Payment(
                payment_id=str(result.payment_id),
                order_id=str(result.order_id),
                amount=payments_pb2.Money(
                    amount=str(result.amount),
                    currency=result.currency,
                ),
                state=_payment_state(result.state),
            )
        )

    def CreatePayment(  # noqa: N802
        self,
        request: service_pb2.CreatePaymentRequest,
        context: grpc.ServicerContext,
    ) -> service_pb2.CreatePaymentResponse:
        subject = self._subjects.resolve(context)
        with _application_errors(context):
            result = self._handlers.create_payment.handle(
                CreatePaymentInput(
                    subject=subject,
                    order_id=request.order_id,
                    amount=request.amount.amount,
                    currency=request.amount.currency,
                )
            )
        return service_pb2.CreatePaymentResponse(
            payment_id=str(result.payment_id),
            state=_payment_state(result.state),
            created=result.created,
        )

    def AuthorizePayment(  # noqa: N802
        self,
        request: service_pb2.AuthorizePaymentRequest,
        context: grpc.ServicerContext,
    ) -> service_pb2.AuthorizePaymentResponse:
        subject = self._subjects.resolve(context)
        with _application_errors(context):
            result = self._handlers.authorize_payment.handle(
                AuthorizePaymentInput(subject, request.payment_id, request.idempotency_key)
            )
        return service_pb2.AuthorizePaymentResponse(operation=_operation(result))

    def CapturePayment(  # noqa: N802
        self,
        request: service_pb2.CapturePaymentRequest,
        context: grpc.ServicerContext,
    ) -> service_pb2.CapturePaymentResponse:
        subject = self._subjects.resolve(context)
        with _application_errors(context):
            result = self._handlers.capture_payment.handle(
                CapturePaymentInput(subject, request.payment_id, request.idempotency_key)
            )
        return service_pb2.CapturePaymentResponse(operation=_operation(result))

    def CancelAuthorization(  # noqa: N802
        self,
        request: service_pb2.CancelAuthorizationRequest,
        context: grpc.ServicerContext,
    ) -> service_pb2.CancelAuthorizationResponse:
        subject = self._subjects.resolve(context)
        with _application_errors(context):
            result = self._handlers.cancel_authorization.handle(
                CancelAuthorizationInput(subject, request.payment_id, request.idempotency_key)
            )
        return service_pb2.CancelAuthorizationResponse(operation=_operation(result))

    def RefundPayment(  # noqa: N802
        self,
        request: service_pb2.RefundPaymentRequest,
        context: grpc.ServicerContext,
    ) -> service_pb2.RefundPaymentResponse:
        subject = self._subjects.resolve(context)
        with _application_errors(context):
            result = self._handlers.refund_payment.handle(
                RefundPaymentInput(subject, request.payment_id, request.idempotency_key)
            )
        return service_pb2.RefundPaymentResponse(operation=_operation(result))


def _abort(context: grpc.ServicerContext, code: grpc.StatusCode, message: str) -> NoReturn:
    context.abort(code, message)


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


class GrpcWorker:
    """grpc.Server implementation of the shared lifecycle Worker protocol."""

    def __init__(
        self,
        address: tuple[str, int],
        handlers: PaymentsHandlers,
        subjects: SubjectResolver,
    ) -> None:
        self._server = grpc.server(futures.ThreadPoolExecutor(thread_name_prefix="payments-grpc"))
        service_pb2_grpc.add_PaymentsServiceServicer_to_server(
            PaymentsService(handlers, subjects), self._server
        )
        host, port = address
        self._port = self._server.add_insecure_port(f"{host}:{port}")
        if self._port == 0:
            raise LifecycleError(f"listen payments gRPC {host}:{port}: address unavailable")

    @property
    def port(self) -> int:
        return self._port

    def start(self) -> None:
        self._server.start()

    def run(self) -> None:
        self._server.wait_for_termination()

    def shutdown(self, deadline: float) -> None:
        remaining = max(0.0, deadline - time.monotonic())
        stopped = self._server.stop(remaining)
        if not stopped.wait(remaining):
            self._server.stop(0)
            raise DrainTimeoutError("payments gRPC requests exceeded drain timeout")


class StorageNotConfigured(RuntimeError):
    """Persistent storage of payments is not connected yet."""


class UnconfiguredPaymentRepository:
    """Finds nothing and refuses writes until persistent storage is connected."""

    def get(self, payment_id):  # type: ignore[no-untyped-def]
        return None

    def get_by_order(self, order_id):  # type: ignore[no-untyped-def]
        return None

    def add(self, payment):  # type: ignore[no-untyped-def]
        raise StorageNotConfigured("payments storage is not configured")

    def save(self, payment):  # type: ignore[no-untyped-def]
        raise StorageNotConfigured("payments storage is not configured")


class UnconfiguredPaymentOperationRepository:
    def get(self, operation_id):  # type: ignore[no-untyped-def]
        return None

    def get_by_idempotency_key(self, idempotency_key):  # type: ignore[no-untyped-def]
        return None

    def find_pending(self, payment_id):  # type: ignore[no-untyped-def]
        return None

    def list_for_payment(self, payment_id):  # type: ignore[no-untyped-def]
        return []

    def add(self, operation):  # type: ignore[no-untyped-def]
        raise StorageNotConfigured("payments storage is not configured")

    def save(self, operation):  # type: ignore[no-untyped-def]
        raise StorageNotConfigured("payments storage is not configured")


class UnconfiguredOutbox:
    def add(self, aggregate, events):  # type: ignore[no-untyped-def]
        raise StorageNotConfigured("payments outbox is not configured")


class PassThroughUnitOfWork:
    """No transaction: every storage write is refused by the unconfigured adapters."""

    def run(self, work: Callable[[], T]) -> T:
        return work()


class UnconfiguredProvider:
    def execute(self, request):  # type: ignore[no-untyped-def]
        raise ProviderUnavailable("payment provider is not configured")


class SystemClock:
    def now(self) -> datetime:
        return datetime.now(timezone.utc)


class UuidIdentifiers:
    def new_payment_id(self) -> PaymentId:
        return PaymentId(str(uuid.uuid4()))

    def new_operation_id(self) -> OperationId:
        return OperationId(str(uuid.uuid4()))


def create_default_handlers(
    permissions: PaymentPermissionChecker | None = None,
) -> PaymentsHandlers:
    """Fail-closed composition until storage and provider adapters exist.

    Without a permission checker every permission is denied (SpiceDB not configured).
    """
    payments = UnconfiguredPaymentRepository()
    if permissions is None:
        permissions = create_permission_checker(None)
    ids = UuidIdentifiers()
    executor = PaymentOperationExecutor(
        uow=PassThroughUnitOfWork(),
        payments=payments,
        operations=UnconfiguredPaymentOperationRepository(),
        outbox=UnconfiguredOutbox(),
        provider=UnconfiguredProvider(),
        permissions=permissions,
        clock=SystemClock(),
        ids=ids,
    )
    return PaymentsHandlers(
        get_payment_status=GetPaymentStatusHandler(payments, permissions),
        create_payment=CreatePaymentHandler(payments, permissions, ids),
        authorize_payment=AuthorizePaymentHandler(executor),
        capture_payment=CapturePaymentHandler(executor),
        cancel_authorization=CancelAuthorizationHandler(executor),
        refund_payment=RefundPaymentHandler(executor),
    )


def create_default_worker(
    address: tuple[str, int],
    identity: IdentitySettings | None = None,
    spicedb: SpiceDBSettings | None = None,
) -> GrpcWorker:
    """Composition root of the gRPC worker.

    Without identity settings, or while issuer, audience or JWKS are not configured,
    every call is rejected ``UNAUTHENTICATED``. An invalid JWKS raises before any port opens.
    Without ``SPICEDB__ENDPOINT`` every permission is denied; otherwise each protected use
    case checks SpiceDB and fails closed (``UNAVAILABLE``) on error or timeout.
    """
    subjects = (
        create_subject_resolver(identity) if identity is not None else RejectingSubjectResolver()
    )
    permissions = create_permission_checker(spicedb)
    return GrpcWorker(address, create_default_handlers(permissions), subjects)
