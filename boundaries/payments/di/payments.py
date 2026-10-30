"""Container of the ``payments`` process: gRPC API and provider webhooks (ADR 5).

Owns process resources, caller identity and the workers; the ports and use cases of the
slice are assembled by ``slices.payment.di.payment.PaymentContainer``.
"""

from dependency_injector import containers, providers

from di.resources import (
    ProcessResource,
    TelemetryResource,
    create_lifecycle,
    listen_all,
    migrated_database,
    telemetry_providers,
)
from infrastructure.grpc.identity import SubjectResolver, create_subject_resolver
from infrastructure.grpc.servicer import PaymentsHandlers
from infrastructure.grpc.worker import GrpcWorker
from pkg.lifecycle import HttpWorker, WorkerGroup
from slices.payment.di.payment import PaymentContainer, spicedb_client
from slices.payment.infrastructure.provider import WebhookVerifier, create_webhook_handler


def create_grpc_worker(
    address: tuple[str, int], subjects: SubjectResolver, handlers: PaymentsHandlers
) -> GrpcWorker:
    """Arguments resolve in order: identity (and JWKS) fails before authorization."""
    return GrpcWorker(address, handlers, subjects)


class PaymentsContainer(containers.DeclarativeContainer):
    """One instance per ``payments`` process; ``config`` holds ``pkg.config.Settings``.

    Identity (``IDENTITY__*``: service tokens and BFF user assertions),
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

    subjects = providers.Singleton(create_subject_resolver, config.identity)
    payment = providers.Container(
        PaymentContainer,
        database=database,
        spicedb=spicedb,
        spicedb_settings=config.spicedb,
        provider_settings=config.payment_provider,
    )

    # Workers.
    grpc_address = providers.Callable(listen_all, config.server.provided.port)
    grpc_worker = providers.Singleton(create_grpc_worker, grpc_address, subjects, payment.handlers)
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
            payment.callbacks,
            payment.clock.provided.now,
        ),
        name="provider webhooks",
    )
    workers = providers.Singleton(WorkerGroup, grpc_worker, webhook_worker)
    lifecycle = providers.Singleton(create_lifecycle, config.management, config.shutdown)
