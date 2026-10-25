"""Composition root of the ``payments`` process: gRPC API and provider webhooks.

The only place that selects implementations of the application ports for this process:
authorization (SpiceDB), payment provider, caller identity, PostgreSQL storage and the
published Integration Events. Handlers are created per process and receive their
dependencies explicitly; ``slices`` never see this module.
"""

from di import resources
from di.webhooks import create_webhook_worker
from infrastructure.authorization import (
    DenyAllPermissions,
    SpiceDBPermissionChecker,
    create_spicedb_client,
)
from infrastructure.grpc.identity import (
    RejectingSubjectResolver,
    SubjectResolver,
    create_subject_resolver,
)
from infrastructure.grpc.service import GrpcWorker, PaymentsHandlers, UuidIdentifiers
from infrastructure.messaging import PaymentEventsMapper
from infrastructure.postgres import (
    NoPublishedContracts,
    OutboxEventMapper,
    PostgresDatabase,
    PostgresPaymentOperationRepository,
    PostgresPaymentOutbox,
    PostgresPaymentRepository,
    PostgresUnitOfWork,
)
from infrastructure.provider import FakePaymentProvider, UnconfiguredProvider
from pkg.application.clock import SystemClock
from pkg.config import IdentitySettings, ProviderSettings, Settings, SpiceDBSettings, load_settings
from pkg.lifecycle import WorkerGroup
from slices.payment.applications.authorize_payment import AuthorizePaymentHandler
from slices.payment.applications.cancel_authorization import CancelAuthorizationHandler
from slices.payment.applications.capture_payment import CapturePaymentHandler
from slices.payment.applications.create_payment import CreatePaymentHandler
from slices.payment.applications.get_payment_status import GetPaymentStatusHandler
from slices.payment.applications.operation_flow import PaymentOperationExecutor
from slices.payment.applications.ports import PaymentPermissionChecker, PaymentProviderGateway
from slices.payment.applications.refund_payment import RefundPaymentHandler


class SpiceDBConfigurationError(RuntimeError):
    """The process checks permissions but SpiceDB is not configured."""


def create_permission_checker(settings: SpiceDBSettings | None) -> PaymentPermissionChecker:
    """Authorization port.

    ``None`` is the test composition without SpiceDB and denies everything. A
    process with settings requires ``SPICEDB__ENDPOINT`` and refuses to start
    without it.
    """
    if settings is None:
        return DenyAllPermissions()
    if not settings.enabled:
        raise SpiceDBConfigurationError(
            "SPICEDB__ENDPOINT is required: payments checks permissions in SpiceDB"
        )
    return SpiceDBPermissionChecker(
        create_spicedb_client(settings), settings.timeout.total_seconds()
    )


def create_provider(settings: ProviderSettings | None) -> PaymentProviderGateway:
    """Payment provider port: the sandbox for ``PROVIDER__KIND=fake``, otherwise none."""
    if settings is not None and settings.kind == "fake":
        return FakePaymentProvider()
    return UnconfiguredProvider()


def create_subject_resolver_or_reject(identity: IdentitySettings | None) -> SubjectResolver:
    """Caller identity; without settings every call is rejected ``UNAUTHENTICATED``."""
    if identity is None:
        return RejectingSubjectResolver()
    return create_subject_resolver(identity)


def create_handlers(
    database: PostgresDatabase,
    permissions: PaymentPermissionChecker | None = None,
    provider: PaymentProviderGateway | None = None,
    outbox_mapper: OutboxEventMapper | None = None,
) -> PaymentsHandlers:
    """Handlers over PostgreSQL storage: one local transaction per Unit of Work.

    Without a permission checker every permission is denied (SpiceDB not configured).
    Without a provider every financial operation is ``UNAVAILABLE``.
    Without an Outbox mapper no Domain Event has a published contract (ADR 31), so
    nothing is written to the Outbox.
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


def create_grpc_worker(
    address: tuple[str, int],
    identity: IdentitySettings | None = None,
    spicedb: SpiceDBSettings | None = None,
    provider: ProviderSettings | None = None,
    *,
    database: PostgresDatabase,
    outbox_mapper: OutboxEventMapper | None = None,
) -> GrpcWorker:
    """gRPC worker of ``PaymentsService``.

    Without identity settings, or while issuer, audience or JWKS are not configured,
    every call is rejected ``UNAUTHENTICATED``. An invalid JWKS raises before any port opens.
    Without ``SPICEDB__ENDPOINT`` every permission is denied; otherwise each protected use
    case checks SpiceDB and fails closed (``UNAVAILABLE``) on error or timeout.
    Without ``PROVIDER__KIND=fake`` every financial operation is ``UNAVAILABLE``.
    Storage is the PostgreSQL ``database`` owned (opened, checked, closed) by the caller.
    ``outbox_mapper`` selects the published Integration Events (none by default).
    """
    subjects = create_subject_resolver_or_reject(identity)
    permissions = create_permission_checker(spicedb)
    return GrpcWorker(
        address,
        create_handlers(database, permissions, create_provider(provider), outbox_mapper),
        subjects,
    )


def serve(settings: Settings) -> None:
    """Own the database for the lifetime of both workers; run them until shutdown."""
    with resources.postgres_database(settings.database) as database:
        # Integration Events payments.v1.payment-events (ADR 31); publishes payments-relay.
        mapper = PaymentEventsMapper()
        worker = create_grpc_worker(
            ("0.0.0.0", settings.server.port),
            settings.identity,
            settings.spicedb,
            settings.provider,
            database=database,
            outbox_mapper=mapper,
        )
        webhooks = create_webhook_worker(
            ("0.0.0.0", settings.provider.webhook_port), settings.provider, database, mapper
        )
        resources.lifecycle(settings.management, settings.shutdown).run(
            WorkerGroup(worker, webhooks)
        )


def run() -> None:
    # Невалидная конфигурация завершает процесс до открытия портов.
    settings = load_settings()
    with resources.telemetry_providers(settings.shutdown.telemetry_flush_timeout):
        serve(settings)
