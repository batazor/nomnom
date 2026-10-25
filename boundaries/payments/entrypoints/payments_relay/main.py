"""payments-relay: публикует Integration Events из Outbox в Kafka (ADR 31).

Отдельный процесс того же образа; активен один экземпляр (advisory lock), остальные —
failover. Пустые DATABASE__URL или KAFKA__BROKERS завершают процесс на старте.
Граф зависимостей — ``di.relay.RelayContainer``.
"""

import logging
import sys

from di.relay import RelayContainer
from di.resources import ProcessResource, TelemetryResource, sections
from pkg.config import load_relay_settings


def run() -> None:
    container = RelayContainer()
    container.config.from_dict(sections(load_relay_settings()))
    container.init_resources(TelemetryResource)
    try:
        container.init_resources(ProcessResource)
        container.lifecycle().run(container.worker())
    finally:
        try:
            container.shutdown_resources(ProcessResource)
        finally:
            container.shutdown_resources(TelemetryResource)


def main() -> int:
    logging.basicConfig(level=logging.INFO, stream=sys.stderr, format="%(asctime)s %(message)s")
    try:
        run()
    except Exception as error:
        print(f"payments-relay: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
