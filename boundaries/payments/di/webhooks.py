"""Provider webhook worker of the ``payments`` process.

Runs in the ``WorkerGroup`` of ``di.payments`` and shares its database; it owns no
process resource of its own.
"""

from infrastructure.postgres import (
    NoPublishedContracts,
    OutboxEventMapper,
    PostgresDatabase,
    PostgresPaymentOperationRepository,
    PostgresPaymentOutbox,
    PostgresPaymentRepository,
    PostgresProviderMessageInbox,
    PostgresUnitOfWork,
)
from infrastructure.provider import WebhookVerifier, create_webhook_handler
from pkg.application.clock import SystemClock
from pkg.config import ProviderSettings
from pkg.lifecycle import HttpWorker
from slices.payment.applications.apply_provider_callback import ApplyProviderCallbackHandler
from slices.payment.applications.operation_flow import ProviderResultApplier


def create_webhook_worker(
    address: tuple[str, int],
    settings: ProviderSettings,
    database: PostgresDatabase,
    outbox_mapper: OutboxEventMapper | None = None,
) -> HttpWorker:
    """Without ``PROVIDER__WEBHOOK_SECRET`` the port opens but every webhook is 400.

    ``outbox_mapper`` selects the published Integration Events (none by default).
    """
    clock = SystemClock()
    applier = ProviderResultApplier(
        PostgresPaymentRepository(database),
        PostgresPaymentOperationRepository(database),
        PostgresPaymentOutbox(database, outbox_mapper or NoPublishedContracts()),
        clock,
    )
    callbacks = ApplyProviderCallbackHandler(
        PostgresUnitOfWork(database), PostgresProviderMessageInbox(database), applier
    )
    verifier = WebhookVerifier(settings.webhook_secret, settings.webhook_tolerance)
    return HttpWorker(
        address, create_webhook_handler(verifier, callbacks, clock.now), name="provider webhooks"
    )
