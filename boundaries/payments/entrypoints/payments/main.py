"""payments process: gRPC API and provider webhooks of the Payment context.

The dependency graph is ``di.payments.PaymentsContainer``.
"""

import logging
import sys

from di.payments import PaymentsContainer
from di.resources import ProcessResource, TelemetryResource, sections
from pkg import telemetry
from pkg.config import PAYMENTS_ROLE, load_settings

logger = logging.getLogger("entrypoints.payments")


def run() -> None:
    """Build the container and run the process lifecycle until a graceful shutdown.

    Invalid configuration, an unreachable database or a failed migration raises before
    any port opens;
    telemetry is initialized first and shut down last.
    """
    # Невалидная конфигурация завершает процесс до открытия портов.
    settings = load_settings()
    telemetry.configure_process_logging(settings.telemetry)
    container = PaymentsContainer()
    container.config.from_dict(sections(settings))
    # Telemetry инициализируется первой и сбрасывается последней (runtime contract, шаг 8).
    container.init_resources(TelemetryResource)
    try:
        # Недоступная база или неудачная миграция завершают процесс до открытия портов:
        # ресурс базы применяет недостающие миграции под lock-ом yoyo.
        container.init_resources(ProcessResource)
        container.lifecycle().run(container.workers())
    finally:
        try:
            container.shutdown_resources(ProcessResource)
        finally:
            container.shutdown_resources(TelemetryResource)


def main() -> int:
    """Console entrypoint; any failure is logged as JSON and turns into exit code 1.

    Logging is configured first: every record, startup errors included, is one JSON
    line on stdout (docs/observability.md#логи). Until the configuration file is loaded the
    records carry only the process name; then ``spec.logging.level`` and the resource apply.
    """
    telemetry.configure_logging(
        {"service.namespace": telemetry.SERVICE_NAMESPACE, "service.name": PAYMENTS_ROLE}
    )
    try:
        run()
    except Exception as error:
        logger.exception("payments: %s", error)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
