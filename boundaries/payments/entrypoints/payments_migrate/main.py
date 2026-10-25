"""Применяет миграции PostgreSQL payments и завершается.

В Kubernetes запускается init container'ом перед процессом; advisory lock сериализует
параллельные запуски реплик. Граф зависимостей — ``di.migrate.MigrateContainer``.
"""

import sys

from di.migrate import MigrateContainer
from pkg.config import DatabaseSettings


def main() -> int:
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
