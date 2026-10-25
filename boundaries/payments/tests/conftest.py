"""Session-wide PostgreSQL for storage tests: one testcontainers instance, migrated once.

Test classes opt in with ``@pytest.mark.usefixtures("postgres")`` and get ``database``
(the shared pool) and ``dsn`` as class attributes. Each test isolates its data by
truncating the tables (``PaymentsTestKit`` and the integration ``setUp`` do it).
"""

from collections.abc import Iterator

import pytest
from testcontainers.community.postgres import PostgresContainer

from infrastructure.postgres import PostgresDatabase, create_database, migrate
from pkg.config import DatabaseSettings

POSTGRES_IMAGE = "postgres:18.6-alpine3.24"


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
