"""payments-admin: rebuild and check of the data derived from the Payment streams.

Runs with the environment of the ``payments`` process; it applies no migrations and opens
no ports (``boundaries/payments/ops/README.md``)::

    python -m entrypoints.payments_admin.main projections check
    python -m entrypoints.payments_admin.main projections rebuild [--snapshots]

Each command prints one JSON line; the exit code is 1 on an error or a found difference.
"""

import argparse
import json
import logging
import sys
from collections.abc import Sequence
from dataclasses import asdict

from infrastructure.repository import create_database
from pkg import telemetry
from pkg.config import EventStoreSettings, database
from slices.payment.infrastructure.repository import ProjectionRebuilder

SERVICE_NAME = "payments-admin"

logger = logging.getLogger("entrypoints.payments_admin")


def parse_args(argv: Sequence[str] | None) -> argparse.Namespace:
    """Parse the command line; an unknown command exits with code 2."""
    parser = argparse.ArgumentParser(prog=SERVICE_NAME)
    groups = parser.add_subparsers(dest="group", required=True)
    projections = groups.add_parser("projections").add_subparsers(dest="command", required=True)
    projections.add_parser("check", help="compare derived data with the streams")
    rebuild = projections.add_parser("rebuild", help="replace the projections from the streams")
    rebuild.add_argument(
        "--snapshots", action="store_true", help="rebuild payment_snapshots as well"
    )
    return parser.parse_args(argv)


def run(args: argparse.Namespace) -> int:
    """Run the command on its own pool; returns the exit code."""
    db = create_database(database.Config())
    try:
        db.check()
        rebuilder = ProjectionRebuilder(db, EventStoreSettings().snapshot_every)
        if args.command == "rebuild":
            result = rebuilder.rebuild(snapshots=args.snapshots)
            sys.stdout.write(json.dumps({"rebuilt": asdict(result)}) + "\n")
            return 0
        mismatches = rebuilder.check()
        sys.stdout.write(json.dumps({"mismatches": [asdict(m) for m in mismatches]}) + "\n")
        return 1 if mismatches else 0
    finally:
        db.close()


def main(argv: Sequence[str] | None = None) -> int:
    """Console entrypoint; any failure is logged as JSON and turns into exit code 1."""
    args = parse_args(argv)
    telemetry.configure_process_logging(SERVICE_NAME)
    try:
        return run(args)
    except Exception as error:
        logger.exception("payments-admin: %s", error)
        return 1


if __name__ == "__main__":
    sys.exit(main())
