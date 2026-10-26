"""Session-wide testcontainers: PostgreSQL and SpiceDB, each started once.

Test classes opt in with ``@pytest.mark.usefixtures("database")`` and get ``database``
(the shared pool) and ``dsn`` as class attributes. Each test isolates its data by
truncating the tables (``PaymentsTestKit`` and the integration ``setUp`` do it).
``spicedb`` sets ``spicedb_endpoint`` of a SpiceDB loaded
with ``authorization/bootstrap.yaml`` (plaintext, preshared key ``SPICEDB_PRESHARED_KEY``).
Images match ``ops/kind``.
"""

from collections.abc import Iterator
from pathlib import Path

import pytest
from testcontainers.community.postgres import PostgresContainer
from testcontainers.core.container import DockerContainer
from testcontainers.core.wait_strategies import LogMessageWaitStrategy

from di.resources import MIGRATIONS
from infrastructure.repository import Database, create_database, migrate
from pkg.config import DatabaseSettings

POSTGRES_IMAGE = "postgres:18.6-alpine3.24"
SPICEDB_IMAGE = "authzed/spicedb:v1.57.0"
SPICEDB_PRESHARED_KEY = "devkey"
AUTHORIZATION_DIR = Path(__file__).resolve().parents[3] / "authorization"


@pytest.fixture(scope="session")
def database_url() -> Iterator[str]:
    with PostgresContainer(
        POSTGRES_IMAGE, username="payments", password="payments", dbname="payments", driver=None
    ) as container:
        dsn = container.get_connection_url()
        migrate(DatabaseSettings(DATABASE__URL=dsn), MIGRATIONS)
        yield dsn


@pytest.fixture(scope="session")
def session_database(database_url: str) -> Iterator[Database]:
    database = create_database(DatabaseSettings(DATABASE__URL=database_url, DATABASE__MAX_CONNS=8))
    database.check()
    yield database
    database.close()


@pytest.fixture(scope="class")
def database(request: pytest.FixtureRequest, database_url: str, session_database: Database) -> None:
    request.cls.dsn = database_url
    request.cls.database = session_database


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
