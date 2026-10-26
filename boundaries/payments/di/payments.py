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
    migrated_database,
    telemetry_providers,
)
from infrastructure.authorization import (
    PermissionsClient,
    SpiceDBPermissionChecker,
    create_spicedb_client,
)
from infrastructure.grpc.identity import SubjectResolver, create_subject_resolver
from infrastructure.grpc.servicer import PaymentsHandlers
from infrastructure.grpc.worker import GrpcWorker
from infrastructure.ids import UuidIdentifiers
from infrastructure.messaging import PaymentEventsMapper
from infrastructure.observability import (
    DomainMetrics,
    FsmMetrics,
    ObservedOutbox,
    ObservedProvider,
    ObservedUnitOfWork,
    classify_payment_error,
)
from infrastructure.observability import usecases as names
from infrastructure.provider import (
    FakePaymentProvider,
    WebhookVerifier,
    create_webhook_handler,
)
from infrastructure.repository import (
    DatabasePaymentOperationRepository,
    DatabasePaymentOutbox,
    DatabasePaymentRepository,
    DatabasePendingOperationQuery,
    DatabaseProviderMessageInbox,
    DatabaseUnitOfWork,
)
from infrastructure.repository.transactions import TransactionMetrics
from pkg.application import Handler
from pkg.application.clock import SystemClock
from pkg.config import ProviderSettings, SpiceDBSettings
from pkg.lifecycle import HttpWorker, WorkerGroup
from pkg.telemetry.usecase import UseCase, UseCases
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


class ProviderConfigurationError(RuntimeError):
    """The payment provider of the process is not configured."""


def spicedb_client(settings: SpiceDBSettings) -> Iterator[PermissionsClient]:
    """The authzed client of the process; ``SPICEDB__ENDPOINT`` is required.

    Channels connect lazily on the first check. The authzed client keeps no handle on
    its channel, so there is nothing to release before the process exits.
    """
    if not settings.enabled:
        raise SpiceDBConfigurationError(
            "SPICEDB__ENDPOINT is required: payments checks permissions in SpiceDB"
        )
    yield create_spicedb_client(settings)


def create_permission_checker(
    settings: SpiceDBSettings, client: PermissionsClient | None = None
) -> PaymentPermissionChecker:
    """Authorization port with nomnom.authz.* per check; ``SPICEDB__ENDPOINT`` is required."""
    if not settings.enabled:
        raise SpiceDBConfigurationError(
            "SPICEDB__ENDPOINT is required: payments checks permissions in SpiceDB"
        )
    return SpiceDBPermissionChecker(
        client if client is not None else create_spicedb_client(settings),
        settings.timeout.total_seconds(),
    )


def create_provider(settings: ProviderSettings) -> PaymentProviderGateway:
    """Payment provider port: the sandbox of ``PROVIDER__KIND=fake``."""
    if settings.kind == "fake":
        return FakePaymentProvider()
    raise ProviderConfigurationError(f"unsupported PROVIDER__KIND {settings.kind!r}")


def instrument[In, Out](
    use_cases: UseCases, use_case: UseCase, handler: Handler[In, Out]
) -> Handler[In, Out]:
    """Use case handler decorated with its span and ``nomnom.usecase.duration``."""
    return use_cases.instrument(use_case, handler)


def create_grpc_worker(
    address: tuple[str, int], subjects: SubjectResolver, handlers: PaymentsHandlers
) -> GrpcWorker:
    """Arguments resolve in order: identity (and JWKS) fails before authorization."""
    return GrpcWorker(address, handlers, subjects)


class PaymentsContainer(containers.DeclarativeContainer):
    """One instance per ``payments`` process; ``config`` holds ``pkg.config.Settings``.

    Identity (``IDENTITY__ISSUER``, ``IDENTITY__AUDIENCE``, ``IDENTITY__JWKS_FILE``),
    ``SPICEDB__ENDPOINT``, ``PROVIDER__KIND`` and ``PROVIDER__WEBHOOK_SECRET`` are
    required: a missing or invalid value stops the process before any port opens.
    """

    # Sections are typed ``pkg.config`` settings, keyed as ``di.resources.sections``.
    config = providers.Configuration(strict=True)

    telemetry = TelemetryResource(
        telemetry_providers, config.shutdown.provided.telemetry_flush_timeout
    )
    database = ProcessResource(migrated_database, config.database)
    spicedb = ProcessResource(spicedb_client, config.spicedb)

    # Ports of the application layer.
    subjects = providers.Singleton(create_subject_resolver, config.identity)
    permissions = providers.Singleton(create_permission_checker, config.spicedb, spicedb)
    # Provider client: span and nomnom.payments.provider.request.duration per request.
    provider = providers.Singleton(
        ObservedProvider, providers.Singleton(create_provider, config.payment_provider)
    )
    # Integration Events payments.v1.payment-events (ADR 31); publishes Debezium (ADR 37).
    outbox_mapper = providers.Singleton(PaymentEventsMapper)
    clock = providers.Singleton(SystemClock)
    ids = providers.Singleton(UuidIdentifiers)
    # Business counters (ops/semconv domain.yaml): Domain Events written to the Outbox
    # are recorded after the outer Unit of Work commits; applied Payment FSM transitions
    # (fsm.yaml) too.
    domain_metrics = providers.Singleton(DomainMetrics)
    fsm_metrics = providers.Singleton(FsmMetrics)
    # Unit of Work: bounded retry on conflicts and nomnom.uow.* per attempt (uow.yaml).
    uow = providers.Singleton(
        ObservedUnitOfWork,
        providers.Singleton(DatabaseUnitOfWork, database, providers.Singleton(TransactionMetrics)),
        domain_metrics,
        fsm_metrics,
    )
    payments = providers.Singleton(DatabasePaymentRepository, database)
    operations = providers.Singleton(DatabasePaymentOperationRepository, database)
    pending = providers.Singleton(DatabasePendingOperationQuery, database)
    outbox = providers.Singleton(
        ObservedOutbox,
        providers.Singleton(DatabasePaymentOutbox, database, outbox_mapper),
        domain_metrics,
    )
    inbox = providers.Singleton(DatabaseProviderMessageInbox, database)
    # One applier of final Provider results for synchronous answers and callbacks.
    applier = providers.Singleton(
        ProviderResultApplier, payments, operations, outbox, clock, fsm_metrics
    )

    # Use cases: one local transaction per Unit of Work.
    executor = providers.Singleton(
        PaymentOperationExecutor,
        uow=uow,
        payments=payments,
        operations=operations,
        pending=pending,
        provider=provider,
        permissions=permissions,
        clock=clock,
        operation_ids=ids,
        applier=applier,
        transitions=fsm_metrics,
    )
    # Every use case is decorated in the composition root: span "usecase <name>" and
    # nomnom.usecase.duration (docs/observability.md#use-cases).
    use_cases = providers.Singleton(UseCases, classify_payment_error)
    handlers = providers.Singleton(
        PaymentsHandlers,
        get_payment_status=providers.Factory(
            instrument,
            use_cases,
            names.GET_PAYMENT_STATUS,
            providers.Factory(GetPaymentStatusHandler, payments, permissions),
        ),
        create_payment=providers.Factory(
            instrument,
            use_cases,
            names.CREATE_PAYMENT,
            providers.Factory(CreatePaymentHandler, payments, permissions, ids),
        ),
        authorize_payment=providers.Factory(
            instrument,
            use_cases,
            names.AUTHORIZE_PAYMENT,
            providers.Factory(AuthorizePaymentHandler, executor),
        ),
        capture_payment=providers.Factory(
            instrument,
            use_cases,
            names.CAPTURE_PAYMENT,
            providers.Factory(CapturePaymentHandler, executor),
        ),
        cancel_authorization=providers.Factory(
            instrument,
            use_cases,
            names.CANCEL_AUTHORIZATION,
            providers.Factory(CancelAuthorizationHandler, executor),
        ),
        refund_payment=providers.Factory(
            instrument,
            use_cases,
            names.REFUND_PAYMENT,
            providers.Factory(RefundPaymentHandler, executor),
        ),
    )
    callbacks = providers.Singleton(
        instrument,
        use_cases,
        names.APPLY_PROVIDER_CALLBACK,
        providers.Singleton(ApplyProviderCallbackHandler, uow, inbox, applier),
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
