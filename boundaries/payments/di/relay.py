"""Container of ``payments-relay``: Outbox → Kafka (ADR 31, ADR 5)."""

from collections.abc import Iterator

from dependency_injector import containers, providers

from di.resources import (
    ProcessResource,
    TelemetryResource,
    create_lifecycle,
    postgres_database,
    seconds,
    telemetry_providers,
)
from infrastructure.messaging import KafkaPublisher, OutboxRelay, PostgresOutboxStore, RelayWorker
from infrastructure.postgres import PostgresDatabase
from pkg.config import KafkaSettings


def kafka_producer(settings: KafkaSettings, database: PostgresDatabase) -> Iterator[KafkaPublisher]:
    """Idempotent producer, opened once the Outbox database is reachable.

    ``database`` only orders the resources: the producer starts after the database
    check and is released before the pool closes. Pending messages are flushed by
    ``RelayWorker.shutdown`` within the drain deadline, so release adds nothing.
    """
    yield KafkaPublisher(settings.brokers, settings.client_id)


SERVICE_NAME = "payments-relay"


class RelayContainer(containers.DeclarativeContainer):
    """One instance per ``payments-relay`` process; ``config`` holds ``RelaySettings``.

    One replica is active (advisory lock), the others are failover.
    """

    config = providers.Configuration(strict=True)

    telemetry = TelemetryResource(
        telemetry_providers, config.shutdown.provided.telemetry_flush_timeout, SERVICE_NAME
    )
    database = ProcessResource(postgres_database, config.database)
    publisher = ProcessResource(kafka_producer, config.kafka, database)

    store = providers.Singleton(PostgresOutboxStore, database, config.database.provided.url)
    relay = providers.Singleton(
        OutboxRelay,
        store,
        publisher,
        batch_size=config.outbox.provided.batch_size,
        retention=config.outbox.provided.retention,
    )
    worker = providers.Singleton(
        RelayWorker,
        relay,
        store,
        publisher,
        batch_size=config.outbox.provided.batch_size,
        poll_interval=providers.Callable(seconds, config.outbox.provided.poll_interval),
    )
    lifecycle = providers.Singleton(create_lifecycle, config.management, config.shutdown)
