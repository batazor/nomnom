"""payments-admin: derived data of the Payment streams, forgetting users, operation history.

Runs with the environment of the ``payments`` process; it applies no migrations and opens
no ports (``boundaries/payments/ops/README.md``)::

    python -m entrypoints.payments_admin.main projections check
    python -m entrypoints.payments_admin.main projections rebuild [--snapshots]
    python -m entrypoints.payments_admin.main subjects forget <user-id>
    python -m entrypoints.payments_admin.main payments history <payment-id>

Each command prints one JSON line; the exit code is 1 on an error or a found difference.
"""

import argparse
import json
import logging
import sys
from collections.abc import Sequence
from dataclasses import asdict
from typing import Any

from infrastructure.repository import create_database
from pkg import telemetry
from pkg.config import EventStoreSettings, database
from slices.payment.domains.payment import OperationRequester, PaymentId, SealedSubject
from slices.payment.infrastructure.repository import (
    DatabasePaymentRepository,
    DatabaseSubjectKeys,
    ProjectionRebuilder,
)

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
    subjects = groups.add_parser("subjects").add_subparsers(dest="command", required=True)
    forget = subjects.add_parser("forget", help="clear the key of a user (crypto-shredding)")
    forget.add_argument("subject_id")
    payments = groups.add_parser("payments").add_subparsers(dest="command", required=True)
    history = payments.add_parser("history", help="print the operations of a Payment")
    history.add_argument("payment_id")
    return parser.parse_args(argv)


def run(args: argparse.Namespace) -> int:
    """Run the command on its own pool; returns the exit code."""
    db = create_database(database.Config())
    settings = EventStoreSettings()
    try:
        db.check()
        match args.group:
            case "projections":
                return _projections(args, ProjectionRebuilder(db, settings.snapshot_every))
            case "subjects":
                forgotten = DatabaseSubjectKeys(db, settings.subject_kek).forget(args.subject_id)
                _print({"forgotten": forgotten})
                return 0
            case "payments":
                return _history(
                    DatabasePaymentRepository(db, settings.snapshot_every),
                    DatabaseSubjectKeys(db, settings.subject_kek),
                    PaymentId(args.payment_id),
                )
            case group:
                raise ValueError(f"unknown command group {group}")
    finally:
        db.close()


def _projections(args: argparse.Namespace, rebuilder: ProjectionRebuilder) -> int:
    if args.command == "rebuild":
        _print({"rebuilt": asdict(rebuilder.rebuild(snapshots=args.snapshots))})
        return 0
    mismatches = rebuilder.check()
    _print({"mismatches": [asdict(m) for m in mismatches]})
    return 1 if mismatches else 0


def _history(
    payments: DatabasePaymentRepository, keys: DatabaseSubjectKeys, payment_id: PaymentId
) -> int:
    payment = payments.get(payment_id)
    if payment is None:
        logger.error("payments-admin: payment %s not found", payment_id)
        return 1
    _print(
        {
            "payment_id": payment.id,
            "state": payment.state.value,
            "operations": [
                {
                    "id": operation.id,
                    "kind": operation.kind.value,
                    "state": operation.state.value,
                    "requested_at": operation.requested_at.isoformat(),
                    "requested_by": _requester(keys, operation.requested_by),
                }
                for operation in payment.operations
            ],
        }
    )
    return 0


def _requester(keys: DatabaseSubjectKeys, requester: OperationRequester | None) -> dict[str, Any]:
    if requester is None:
        return {"status": "unknown"}
    identifier = (
        keys.reveal(requester.id) if isinstance(requester.id, SealedSubject) else requester.id
    )
    return {
        "status": "forgotten" if identifier is None else "known",
        "kind": requester.kind.value,
        "id": identifier,
        "delegate": requester.delegate,
    }


def _print(value: dict[str, Any]) -> None:
    sys.stdout.write(json.dumps(value) + "\n")


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
