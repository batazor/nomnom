"""Forward-only SQL migrations of the payments database.

Files ``migrations/<version>_<name>.sql`` are applied in version order, each in its
own transaction together with its ``schema_migrations`` row. A session advisory lock
serializes concurrent runs (several replicas starting their init containers).
"""

import re
from importlib import resources

import psycopg

_FILE = re.compile(r"^(\d+)_[\w-]+\.sql$")
# Arbitrary constant shared by every payments migrator.
_LOCK_KEY = 0x7061796D656E7473  # "payments"


def migrations() -> list[tuple[int, str, str]]:
    """Return the bundled migrations as ``(version, file name, SQL)`` sorted by version."""
    found = []
    for entry in resources.files(__package__).joinpath("migrations").iterdir():
        match = _FILE.match(entry.name)
        if match is not None:
            found.append((int(match.group(1)), entry.name, entry.read_text(encoding="utf-8")))
    return sorted(found)


def migrate(dsn: str) -> list[str]:
    """Apply pending migrations and return the names of the applied files."""
    applied: list[str] = []
    with psycopg.connect(dsn, autocommit=True) as conn:
        conn.execute("SELECT pg_advisory_lock(%s)", (_LOCK_KEY,))
        try:
            conn.execute(
                "CREATE TABLE IF NOT EXISTS schema_migrations ("
                "version BIGINT PRIMARY KEY, name TEXT NOT NULL, "
                "applied_at TIMESTAMPTZ NOT NULL DEFAULT now())"
            )
            done = {row[0] for row in conn.execute("SELECT version FROM schema_migrations")}
            for version, name, sql in migrations():
                if version in done:
                    continue
                with conn.transaction():
                    conn.execute(sql.encode())
                    conn.execute(
                        "INSERT INTO schema_migrations (version, name) VALUES (%s, %s)",
                        (version, name),
                    )
                applied.append(name)
        finally:
            conn.execute("SELECT pg_advisory_unlock(%s)", (_LOCK_KEY,))
    return applied
