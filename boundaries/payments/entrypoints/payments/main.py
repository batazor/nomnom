import logging
import sys

from infrastructure.grpc.service import create_default_worker
from infrastructure.messaging import PaymentEventsMapper
from infrastructure.postgres import create_database
from infrastructure.provider import create_webhook_worker
from pkg import telemetry
from pkg.config import Settings, load_settings
from pkg.lifecycle import Lifecycle, LifecycleConfig, WorkerGroup


def run() -> None:
    # Невалидная конфигурация завершает процесс до открытия портов.
    settings = load_settings()
    providers = telemetry.setup()
    try:
        serve(settings)
    finally:
        providers.shutdown(settings.shutdown.telemetry_flush_timeout.total_seconds())


def serve(settings: Settings) -> None:
    database = create_database(settings.database)
    try:
        # Недоступная база завершает процесс до открытия портов; схему применяет
        # entrypoints.payments_migrate (init container).
        database.check()
        # Integration Events payments.v1.payment-events (ADR 31); publishes payments-relay.
        mapper = PaymentEventsMapper()
        worker = create_default_worker(
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
        Lifecycle(
            LifecycleConfig(
                management_port=settings.management.port,
                readiness_delay=settings.shutdown.readiness_delay.total_seconds(),
                drain_timeout=settings.shutdown.drain_timeout.total_seconds(),
            ),
        ).run(WorkerGroup(worker, webhooks))
    finally:
        database.close()


def main() -> int:
    logging.basicConfig(level=logging.INFO, stream=sys.stderr, format="%(asctime)s %(message)s")
    try:
        run()
    except Exception as error:
        print(f"payments: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
