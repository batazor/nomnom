"""Применяет миграции PostgreSQL payments и завершается.

В Kubernetes запускается init container'ом перед процессом; advisory lock сериализует
параллельные запуски реплик. Composition root — ``di.migrate``.
"""

import sys

from di import migrate


def main() -> int:
    try:
        applied = migrate.run()
    except Exception as error:
        print(f"payments-migrate: {error}", file=sys.stderr)
        return 1
    print(f"payments migrations applied: {', '.join(applied) or 'none pending'}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
