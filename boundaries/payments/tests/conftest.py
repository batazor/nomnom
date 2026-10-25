"""Session-wide testcontainers: PostgreSQL, Kafka and SpiceDB, each started once.

Test classes opt in with ``@pytest.mark.usefixtures("postgres")`` and get ``database``
(the shared pool) and ``dsn`` as class attributes. Each test isolates its data by
truncating the tables (``PaymentsTestKit`` and the integration ``setUp`` do it).
``kafka`` sets ``brokers``; ``spicedb`` sets ``spicedb_endpoint`` of a SpiceDB loaded
with ``authorization/bootstrap.yaml`` (plaintext, preshared key ``SPICEDB_PRESHARED_KEY``).
Images match ``ops/kind``.
"""

import socket
from collections.abc import Iterator
from pathlib import Path

import pytest
from testcontainers.community.postgres import PostgresContainer
from testcontainers.core.container import DockerContainer
from testcontainers.core.wait_strategies import LogMessageWaitStrategy

from infrastructure.postgres import PostgresDatabase, create_database, migrate
from pkg.config import DatabaseSettings

POSTGRES_IMAGE = "postgres:18.6-alpine3.24"
KAFKA_IMAGE = "apache/kafka:4.4.0"
SPICEDB_IMAGE = "authzed/spicedb:v1.57.0"
SPICEDB_PRESHARED_KEY = "devkey"
AUTHORIZATION_DIR = Path(__file__).resolve().parents[3] / "authorization"


@pytest.fixture(scope="session")
def postgres_dsn() -> Iterator[str]:
    with PostgresContainer(
        POSTGRES_IMAGE, username="payments", password="payments", dbname="payments", driver=None
    ) as container:
        dsn = container.get_connection_url()
        migrate(dsn)
        yield dsn


@pytest.fixture(scope="session")
def postgres_database(postgres_dsn: str) -> Iterator[PostgresDatabase]:
    database = create_database(DatabaseSettings(DATABASE__URL=postgres_dsn, DATABASE__MAX_CONNS=8))
    database.check()
    yield database
    database.close()


@pytest.fixture(scope="class")
def postgres(
    request: pytest.FixtureRequest, postgres_dsn: str, postgres_database: PostgresDatabase
) -> None:
    request.cls.dsn = postgres_dsn
    request.cls.database = postgres_database


@pytest.fixture(scope="session")
def kafka_brokers() -> Iterator[str]:
    # The advertised listener must name the host port, so it is chosen before start.
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        port = probe.getsockname()[1]
    container = (
        DockerContainer(KAFKA_IMAGE)
        .with_bind_ports(9092, port)
        .with_env("KAFKA_NODE_ID", "1")
        .with_env("KAFKA_PROCESS_ROLES", "broker,controller")
        .with_env("KAFKA_LISTENERS", "PLAINTEXT://:9092,CONTROLLER://:9093")
        .with_env("KAFKA_ADVERTISED_LISTENERS", f"PLAINTEXT://127.0.0.1:{port}")
        .with_env("KAFKA_CONTROLLER_LISTENER_NAMES", "CONTROLLER")
        .with_env(
            "KAFKA_LISTENER_SECURITY_PROTOCOL_MAP", "CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT"
        )
        .with_env("KAFKA_CONTROLLER_QUORUM_VOTERS", "1@localhost:9093")
        .with_env("KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR", "1")
        .with_env("KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR", "1")
        .with_env("KAFKA_TRANSACTION_STATE_LOG_MIN_ISR", "1")
        .waiting_for(LogMessageWaitStrategy("Kafka Server started").with_startup_timeout(120))
    )
    with container:
        yield f"127.0.0.1:{port}"


@pytest.fixture(scope="class")
def kafka(request: pytest.FixtureRequest, kafka_brokers: str) -> None:
    request.cls.brokers = kafka_brokers


@pytest.fixture(scope="session")
def spicedb_endpoint() -> Iterator[str]:
    # ``serve`` with the memory datastore, not ``serve-testing``: the latter ignores
    # the preshared key, and the wrong-key test needs it enforced.
    container = (
        DockerContainer(SPICEDB_IMAGE)
        .with_volume_mapping(str(AUTHORIZATION_DIR), "/authorization", "ro")
        # SpiceDB resolves ``schemaFile`` only inside the working directory.
        .with_kwargs(working_dir="/authorization")
        .with_command(
            "serve --datastore-engine memory"
            f" --grpc-preshared-key {SPICEDB_PRESHARED_KEY}"
            " --datastore-bootstrap-files bootstrap.yaml"
        )
        .with_exposed_ports(50051)
        .waiting_for(LogMessageWaitStrategy("grpc server started serving").with_startup_timeout(60))
    )
    with container:
        yield f"{container.get_container_host_ip()}:{container.get_exposed_port(50051)}"


@pytest.fixture(scope="class")
def spicedb(request: pytest.FixtureRequest, spicedb_endpoint: str) -> None:
    request.cls.spicedb_endpoint = spicedb_endpoint
