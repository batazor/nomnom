"""Apply the payments PostgreSQL migrations and exit.

In Kubernetes it runs as an init container before the process; an advisory lock
serializes concurrent runs of replicas. The dependency graph is
``di.migrate.MigrateContainer``.
"""

import logging
import sys

from di.migrate import MigrateContainer
from pkg import telemetry
from pkg.config import DatabaseSettings

SERVICE_NAME = "payments-migrate"

logger = logging.getLogger("entrypoints.payments_migrate")


def main() -> int:
    """Console entrypoint; returns 1 and logs the error (JSON on stdout) when migration fails."""
    telemetry.configure_process_logging(SERVICE_NAME)
    try:
        container = MigrateContainer()
        container.config.from_dict({"database": DatabaseSettings()})
        applied = container.migrations()
    except Exception as error:
        logger.exception("payments-migrate: %s", error)
        return 1
    logger.info("payments migrations applied: %s", ", ".join(applied) or "none pending")
    return 0


if __name__ == "__main__":
    sys.exit(main())
