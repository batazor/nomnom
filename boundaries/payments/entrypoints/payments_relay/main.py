"""payments-relay: публикует Integration Events из Outbox в Kafka (ADR 31).

Отдельный процесс того же образа; активен один экземпляр (advisory lock), остальные —
failover. Пустые DATABASE__URL или KAFKA__BROKERS завершают процесс на старте.
"""

import logging
import sys

from infrastructure.messaging import KafkaPublisher, OutboxRelay, PostgresOutboxStore, RelayWorker
from infrastructure.postgres import create_database
from pkg import telemetry
from pkg.config import RelaySettings, load_relay_settings
from pkg.lifecycle import Lifecycle, LifecycleConfig


def run() -> None:
    settings = load_relay_settings()
    providers = telemetry.setup()
    try:
        serve(settings)
    finally:
        providers.shutdown(settings.shutdown.telemetry_flush_timeout.total_seconds())


def serve(settings: RelaySettings) -> None:
    database = create_database(settings.database)
    try:
        database.check()
        store = PostgresOutboxStore(database, settings.database.url)
        publisher = KafkaPublisher(settings.kafka.brokers, settings.kafka.client_id)
        relay = OutboxRelay(
            store,
            publisher,
            batch_size=settings.outbox.batch_size,
            retention=settings.outbox.retention,
        )
        worker = RelayWorker(
            relay,
            store,
            publisher,
            batch_size=settings.outbox.batch_size,
            poll_interval=settings.outbox.poll_interval.total_seconds(),
        )
        Lifecycle(
            LifecycleConfig(
                management_port=settings.management.port,
                readiness_delay=settings.shutdown.readiness_delay.total_seconds(),
                drain_timeout=settings.shutdown.drain_timeout.total_seconds(),
            ),
        ).run(worker)
    finally:
        database.close()


def main() -> int:
    logging.basicConfig(level=logging.INFO, stream=sys.stderr, format="%(asctime)s %(message)s")
    try:
        run()
    except Exception as error:
        print(f"payments-relay: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
