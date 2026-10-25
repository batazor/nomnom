"""payments-relay: publishes Integration Events from the Outbox to Kafka (ADR 31).

A separate process of the same image; one instance is active (advisory lock), the
others stand by for failover. An empty DATABASE__URL or KAFKA__BROKERS stops the process
at startup. The dependency graph is ``di.relay.RelayContainer``.
"""

import logging
import sys

from di.relay import RelayContainer
from di.resources import ProcessResource, TelemetryResource, sections
from pkg.config import load_relay_settings


def run() -> None:
    """Build the relay container and run the process lifecycle until a graceful shutdown.

    Telemetry is initialized first and shut down last.
    """
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
    """Console entrypoint; any failure is printed to stderr and turns into exit code 1."""
    logging.basicConfig(level=logging.INFO, stream=sys.stderr, format="%(asctime)s %(message)s")
    try:
        run()
    except Exception as error:
        print(f"payments-relay: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
