"""payments-relay: публикует Integration Events из Outbox в Kafka (ADR 31).

Отдельный процесс того же образа; активен один экземпляр (advisory lock), остальные —
failover. Пустые DATABASE__URL или KAFKA__BROKERS завершают процесс на старте.
Composition root — ``di.relay``.
"""

import logging
import sys

from di import relay


def main() -> int:
    logging.basicConfig(level=logging.INFO, stream=sys.stderr, format="%(asctime)s %(message)s")
    try:
        relay.run()
    except Exception as error:
        print(f"payments-relay: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
