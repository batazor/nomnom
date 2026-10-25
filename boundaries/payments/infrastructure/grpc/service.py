"""grpcio adapter for ``nomnom.payments.v1.PaymentsService``."""

import time
import uuid
from concurrent import futures

import grpc

from infrastructure.authorization import create_permission_checker
from infrastructure.grpc.identity import (
    RejectingSubjectResolver,
    SubjectResolver,
    create_subject_resolver,
)
from infrastructure.grpc.servicer import (
    PaymentsHandlers,
    PaymentsService,
    payments_pb2,
    service_pb2,
    service_pb2_grpc,
)
from infrastructure.postgres import (
    NoPublishedContracts,
    OutboxEventMapper,
    PostgresDatabase,
    PostgresPaymentOperationRepository,
    PostgresPaymentOutbox,
    PostgresPaymentRepository,
    PostgresUnitOfWork,
)
from infrastructure.provider import create_provider
from pkg.application.clock import SystemClock
from pkg.config import IdentitySettings, ProviderSettings, SpiceDBSettings
from pkg.lifecycle import DrainTimeoutError, LifecycleError
from slices.payment.applications.authorize_payment import AuthorizePaymentHandler
from slices.payment.applications.cancel_authorization import CancelAuthorizationHandler
from slices.payment.applications.capture_payment import CapturePaymentHandler
from slices.payment.applications.create_payment import CreatePaymentHandler
from slices.payment.applications.get_payment_status import GetPaymentStatusHandler
from slices.payment.applications.operation_flow import PaymentOperationExecutor
from slices.payment.applications.ports import PaymentPermissionChecker, PaymentProviderGateway
from slices.payment.applications.refund_payment import RefundPaymentHandler
from slices.payment.domains.payment import OperationId, PaymentId

__all__ = [
    "GrpcWorker",
    "PaymentsHandlers",
    "PaymentsService",
    "SubjectResolver",
    "UuidIdentifiers",
    "create_default_handlers",
    "create_default_worker",
    "payments_pb2",
    "service_pb2",
    "service_pb2_grpc",
]


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


class UuidIdentifiers:
    def new_payment_id(self) -> PaymentId:
        return PaymentId(str(uuid.uuid4()))

    def new_operation_id(self) -> OperationId:
        return OperationId(str(uuid.uuid4()))


def create_default_handlers(
    database: PostgresDatabase,
    permissions: PaymentPermissionChecker | None = None,
    provider: PaymentProviderGateway | None = None,
    outbox_mapper: OutboxEventMapper | None = None,
) -> PaymentsHandlers:
    """Handlers over PostgreSQL storage: one local transaction per Unit of Work.

    Without a permission checker every permission is denied (SpiceDB not configured).
    Without an Outbox mapper no Domain Event has a published contract (ADR 31), so
    nothing is written to the Outbox until the Protobuf mappers are injected here.
    """
    payments = PostgresPaymentRepository(database)
    if permissions is None:
        permissions = create_permission_checker(None)
    ids = UuidIdentifiers()
    executor = PaymentOperationExecutor(
        uow=PostgresUnitOfWork(database),
        payments=payments,
        operations=PostgresPaymentOperationRepository(database),
        outbox=PostgresPaymentOutbox(database, outbox_mapper or NoPublishedContracts()),
        provider=provider if provider is not None else create_provider(None),
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
    provider: ProviderSettings | None = None,
    *,
    database: PostgresDatabase,
    outbox_mapper: OutboxEventMapper | None = None,
) -> GrpcWorker:
    """Composition root of the gRPC worker.

    Without identity settings, or while issuer, audience or JWKS are not configured,
    every call is rejected ``UNAUTHENTICATED``. An invalid JWKS raises before any port opens.
    Without ``SPICEDB__ENDPOINT`` every permission is denied; otherwise each protected use
    case checks SpiceDB and fails closed (``UNAVAILABLE``) on error or timeout.
    Without ``PROVIDER__KIND=fake`` every financial operation is ``UNAVAILABLE``.
    Storage is the PostgreSQL ``database`` owned (opened, checked, closed) by the caller.
    ``outbox_mapper`` selects the published Integration Events (none by default).
    """
    subjects = (
        create_subject_resolver(identity) if identity is not None else RejectingSubjectResolver()
    )
    permissions = create_permission_checker(spicedb)
    return GrpcWorker(
        address,
        create_default_handlers(database, permissions, create_provider(provider), outbox_mapper),
        subjects,
    )
