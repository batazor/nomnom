"""Composition root of ``payments-relay``: Outbox → Kafka (ADR 31).

Owns the database for the process lifetime. The Kafka producer is created here and
flushed by ``RelayWorker.shutdown`` as part of the drain contract.
"""

from di import resources
from infrastructure.messaging import KafkaPublisher, OutboxRelay, PostgresOutboxStore, RelayWorker
from infrastructure.postgres import PostgresDatabase
from pkg.config import RelaySettings, load_relay_settings


def create_relay_worker(settings: RelaySettings, database: PostgresDatabase) -> RelayWorker:
    store = PostgresOutboxStore(database, settings.database.url)
    publisher = KafkaPublisher(settings.kafka.brokers, settings.kafka.client_id)
    relay = OutboxRelay(
        store,
        publisher,
        batch_size=settings.outbox.batch_size,
        retention=settings.outbox.retention,
    )
    return RelayWorker(
        relay,
        store,
        publisher,
        batch_size=settings.outbox.batch_size,
        poll_interval=settings.outbox.poll_interval.total_seconds(),
    )


def serve(settings: RelaySettings) -> None:
    with resources.postgres_database(settings.database) as database:
        worker = create_relay_worker(settings, database)
        resources.lifecycle(settings.management, settings.shutdown).run(worker)


def run() -> None:
    settings = load_relay_settings()
    with resources.telemetry_providers(settings.shutdown.telemetry_flush_timeout):
        serve(settings)
