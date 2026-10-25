import logging
import sys

from di.payments import PaymentsContainer
from di.resources import ProcessResource, TelemetryResource, sections
from pkg.config import load_settings


def run() -> None:
    # Невалидная конфигурация завершает процесс до открытия портов.
    container = PaymentsContainer()
    container.config.from_dict(sections(load_settings()))
    # Telemetry инициализируется первой и сбрасывается последней (runtime contract, шаг 8).
    container.init_resources(TelemetryResource)
    try:
        # Недоступная база завершает процесс до открытия портов; схему применяет
        # entrypoints.payments_migrate (init container).
        container.init_resources(ProcessResource)
        container.lifecycle().run(container.workers())
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
        print(f"payments: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
