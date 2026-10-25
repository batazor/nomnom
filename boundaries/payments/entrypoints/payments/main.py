import logging
import sys

from infrastructure.grpc.service import create_default_worker
from pkg.config import load_settings
from pkg.lifecycle import Lifecycle, LifecycleConfig


def run() -> None:
    # Невалидная конфигурация завершает процесс до открытия портов.
    settings = load_settings()

    worker = create_default_worker(("0.0.0.0", settings.server.port), settings.identity)
    Lifecycle(
        LifecycleConfig(
            management_port=settings.management.port,
            readiness_delay=settings.shutdown.readiness_delay.total_seconds(),
            drain_timeout=settings.shutdown.drain_timeout.total_seconds(),
        ),
    ).run(worker)


def main() -> int:
    logging.basicConfig(level=logging.INFO, stream=sys.stderr, format="%(asctime)s %(message)s")
    try:
        run()
    except Exception as error:
        print(f"payments: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
