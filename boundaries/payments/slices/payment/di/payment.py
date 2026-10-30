"""Container of the payment slice: its ports, adapters and use cases (ADR 5).

The only place that selects implementations of the application ports of the slice:
authorization (SpiceDB), payment provider, PostgreSQL storage and the published Integration
Events. Handlers receive their dependencies explicitly through constructors; applications
never see the container and nothing is wired.
"""

from collections.abc import Iterator

from dependency_injector import containers, providers

from infrastructure.grpc.servicer import PaymentsHandlers
from infrastructure.repository import DatabaseUnitOfWork
from infrastructure.repository.transactions import TransactionMetrics
from pkg.config import ProviderSettings, SpiceDBSettings
from pkg.telemetry.usecase import UseCase, UseCases
from pkg.usecase import Handler
from pkg.usecase.clock import SystemClock
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
from slices.payment.infrastructure.authorization import (
    PermissionsClient,
    SpiceDBPermissionChecker,
    create_spicedb_client,
)
from slices.payment.infrastructure.ids import UuidIdentifiers
from slices.payment.infrastructure.messaging import PaymentEventsMapper
from slices.payment.infrastructure.observability import (
    DomainMetrics,
    FsmMetrics,
    ObservedOutbox,
    ObservedProvider,
    ObservedUnitOfWork,
    classify_payment_error,
)
from slices.payment.infrastructure.observability import usecases as names
from slices.payment.infrastructure.provider import (
    FakePaymentProvider,
)
from slices.payment.infrastructure.repository import (
    DatabasePaymentOutbox,
    DatabasePaymentRepository,
    DatabasePaymentStatusReader,
    DatabaseProviderMessageInbox,
)


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


class PaymentContainer(containers.DeclarativeContainer):
    """Dependencies come from the process container ``di.payments.PaymentsContainer``."""

    database = providers.Dependency()
    spicedb = providers.Dependency()
    spicedb_settings = providers.Dependency()
    provider_settings = providers.Dependency()
    event_store_settings = providers.Dependency()

    # Ports of the application layer.
    permissions = providers.Singleton(create_permission_checker, spicedb_settings, spicedb)
    # Provider client: span and nomnom.payments.provider.request.duration per request.
    payment_provider = providers.Singleton(
        ObservedProvider, providers.Singleton(create_provider, provider_settings)
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
    # Streams of Payment: the source of truth with synchronous projections (local ADR 2).
    payments = providers.Singleton(
        DatabasePaymentRepository, database, event_store_settings.provided.snapshot_every
    )
    statuses = providers.Singleton(DatabasePaymentStatusReader, database)
    outbox = providers.Singleton(
        ObservedOutbox,
        providers.Singleton(DatabasePaymentOutbox, database, outbox_mapper),
        domain_metrics,
    )
    inbox = providers.Singleton(DatabaseProviderMessageInbox, database)
    # One applier of final Provider results for synchronous answers and callbacks.
    applier = providers.Singleton(ProviderResultApplier, payments, outbox, clock, fsm_metrics)

    # Use cases: one local transaction per Unit of Work.
    executor = providers.Singleton(
        PaymentOperationExecutor,
        uow=uow,
        payments=payments,
        provider=payment_provider,
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
            providers.Factory(GetPaymentStatusHandler, statuses, permissions),
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
