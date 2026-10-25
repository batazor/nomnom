"""Container of the ``payments`` process: gRPC API and provider webhooks (ADR 5).

The only place that selects implementations of the application ports for this process:
caller identity, authorization (SpiceDB), payment provider, PostgreSQL storage and the
published Integration Events. Handlers receive their dependencies explicitly through
constructors; ``slices`` never see the container and nothing is wired.
"""

from collections.abc import Iterator

from dependency_injector import containers, providers

from di.resources import (
    ProcessResource,
    TelemetryResource,
    create_lifecycle,
    listen_all,
    postgres_database,
    telemetry_providers,
)
from infrastructure.authorization import (
    DenyAllPermissions,
    PermissionsClient,
    SpiceDBPermissionChecker,
    create_spicedb_client,
)
from infrastructure.grpc.identity import SubjectResolver, create_subject_resolver
from infrastructure.grpc.servicer import PaymentsHandlers
from infrastructure.grpc.worker import GrpcWorker
from infrastructure.ids import UuidIdentifiers
from infrastructure.messaging import PaymentEventsMapper
from infrastructure.observability import DomainMetrics, ObservedOutbox, ObservedUnitOfWork
from infrastructure.postgres import (
    PostgresPaymentOperationRepository,
    PostgresPaymentOutbox,
    PostgresPaymentRepository,
    PostgresProviderMessageInbox,
    PostgresUnitOfWork,
)
from infrastructure.provider import (
    FakePaymentProvider,
    UnconfiguredProvider,
    WebhookVerifier,
    create_webhook_handler,
)
from pkg.application.clock import SystemClock
from pkg.config import ProviderSettings, SpiceDBSettings
from pkg.lifecycle import HttpWorker, WorkerGroup
from slices.payment.applications.apply_provider_callback import ApplyProviderCallbackHandler
from slices.payment.applications.authorize_payment import AuthorizePaymentHandler
from slices.payment.applications.cancel_authorization import CancelAuthorizationHandler
from slices.payment.applications.capture_payment import CapturePaymentHandler
from slices.payment.applications.create_payment import CreatePaymentHandler
from slices.payment.applications.get_payment_status import GetPaymentStatusHandler
from slices.payment.applications.operation_flow import (
    PaymentOperationExecutor,
    ProviderResultApplier,
)
from slices.payment.applications.ports import PaymentPermissionChecker, PaymentProviderGateway
from slices.payment.applications.refund_payment import RefundPaymentHandler


class SpiceDBConfigurationError(RuntimeError):
    """The process checks permissions but SpiceDB is not configured."""


def spicedb_client(settings: SpiceDBSettings) -> Iterator[PermissionsClient | None]:
    """The authzed client of the process; ``None`` while ``SPICEDB__ENDPOINT`` is empty.

    Channels connect lazily on the first check. The authzed client keeps no handle on
    its channel, so there is nothing to release before the process exits.
    """
    yield create_spicedb_client(settings) if settings.enabled else None


def create_permission_checker(
    settings: SpiceDBSettings | None, client: PermissionsClient | None = None
) -> PaymentPermissionChecker:
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
        client if client is not None else create_spicedb_client(settings),
        settings.timeout.total_seconds(),
    )


def create_provider(settings: ProviderSettings | None) -> PaymentProviderGateway:
    """Payment provider port: the sandbox for ``PROVIDER__KIND=fake``, otherwise none."""
    if settings is not None and settings.kind == "fake":
        return FakePaymentProvider()
    return UnconfiguredProvider()


def create_grpc_worker(
    address: tuple[str, int], subjects: SubjectResolver, handlers: PaymentsHandlers
) -> GrpcWorker:
    """Arguments resolve in order: identity (and JWKS) fails before authorization."""
    return GrpcWorker(address, handlers, subjects)


class PaymentsContainer(containers.DeclarativeContainer):
    """One instance per ``payments`` process; ``config`` holds ``pkg.config.Settings``.

    Without ``IDENTITY__ISSUER``, ``IDENTITY__AUDIENCE`` or ``IDENTITY__JWKS_FILE`` every
    call is rejected ``UNAUTHENTICATED``; an invalid JWKS raises before any port opens.
    An empty ``SPICEDB__ENDPOINT`` stops the process; otherwise each protected use case
    checks SpiceDB and fails closed (``UNAVAILABLE``). Without ``PROVIDER__KIND=fake``
    every financial operation is ``UNAVAILABLE``. Without ``PROVIDER__WEBHOOK_SECRET``
    the webhook port opens but every webhook is 400.
    """

    # Sections are typed ``pkg.config`` settings, keyed as ``di.resources.sections``.
    config = providers.Configuration(strict=True)

    telemetry = TelemetryResource(
        telemetry_providers, config.shutdown.provided.telemetry_flush_timeout
    )
    database = ProcessResource(postgres_database, config.database)
    spicedb = ProcessResource(spicedb_client, config.spicedb)

    # Ports of the application layer.
    subjects = providers.Singleton(create_subject_resolver, config.identity)
    permissions = providers.Singleton(create_permission_checker, config.spicedb, spicedb)
    provider = providers.Singleton(create_provider, config.payment_provider)
    # Integration Events payments.v1.payment-events (ADR 31); publishes payments-relay.
    outbox_mapper = providers.Singleton(PaymentEventsMapper)
    clock = providers.Singleton(SystemClock)
    ids = providers.Singleton(UuidIdentifiers)
    # Business counters (ops/semconv domain.yaml): Domain Events written to the Outbox
    # are recorded after the outer Unit of Work commits.
    domain_metrics = providers.Singleton(DomainMetrics)
    uow = providers.Singleton(
        ObservedUnitOfWork,
        providers.Singleton(PostgresUnitOfWork, database),
        domain_metrics,
    )
    payments = providers.Singleton(PostgresPaymentRepository, database)
    operations = providers.Singleton(PostgresPaymentOperationRepository, database)
    outbox = providers.Singleton(
        ObservedOutbox,
        providers.Singleton(PostgresPaymentOutbox, database, outbox_mapper),
        domain_metrics,
    )
    inbox = providers.Singleton(PostgresProviderMessageInbox, database)
    # One applier of final Provider results for synchronous answers and callbacks.
    applier = providers.Singleton(ProviderResultApplier, payments, operations, outbox, clock)

    # Use cases: one local transaction per Unit of Work.
    executor = providers.Singleton(
        PaymentOperationExecutor,
        uow=uow,
        payments=payments,
        operations=operations,
        provider=provider,
        permissions=permissions,
        clock=clock,
        operation_ids=ids,
        applier=applier,
    )
    handlers = providers.Singleton(
        PaymentsHandlers,
        get_payment_status=providers.Factory(GetPaymentStatusHandler, payments, permissions),
        create_payment=providers.Factory(CreatePaymentHandler, payments, permissions, ids),
        authorize_payment=providers.Factory(AuthorizePaymentHandler, executor),
        capture_payment=providers.Factory(CapturePaymentHandler, executor),
        cancel_authorization=providers.Factory(CancelAuthorizationHandler, executor),
        refund_payment=providers.Factory(RefundPaymentHandler, executor),
    )
    callbacks = providers.Singleton(
        ApplyProviderCallbackHandler,
        uow,
        inbox,
        applier,
    )

    # Workers.
    grpc_address = providers.Callable(listen_all, config.server.provided.port)
    grpc_worker = providers.Singleton(create_grpc_worker, grpc_address, subjects, handlers)
    webhook_address = providers.Callable(listen_all, config.payment_provider.provided.webhook_port)
    webhook_worker = providers.Singleton(
        HttpWorker,
        webhook_address,
        providers.Callable(
            create_webhook_handler,
            providers.Factory(
                WebhookVerifier,
                config.payment_provider.provided.webhook_secret,
                config.payment_provider.provided.webhook_tolerance,
            ),
            callbacks,
            clock.provided.now,
        ),
        name="provider webhooks",
    )
    workers = providers.Singleton(WorkerGroup, grpc_worker, webhook_worker)
    lifecycle = providers.Singleton(create_lifecycle, config.management, config.shutdown)
