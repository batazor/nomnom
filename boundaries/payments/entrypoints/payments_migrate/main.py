"""Apply the payments PostgreSQL migrations and exit.

In Kubernetes it runs as an init container before the process; an advisory lock
serializes concurrent runs of replicas. The dependency graph is
``di.migrate.MigrateContainer``.
"""

import sys

from di.migrate import MigrateContainer
from pkg.config import DatabaseSettings


def main() -> int:
    """Console entrypoint; returns 1 and prints the error to stderr when migration fails."""
    try:
        container = MigrateContainer()
        container.config.from_dict({"database": DatabaseSettings()})
        applied = container.migrations()
    except Exception as error:
        print(f"payments-migrate: {error}", file=sys.stderr)
        return 1
    print(f"payments migrations applied: {', '.join(applied) or 'none pending'}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
