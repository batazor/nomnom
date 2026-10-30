"""Recorded streams stay readable and snapshots never change the result (FR-745, FR-747).

``fixtures/payment_streams`` is written once and never edited: a change of events, schemas
or ``evolve`` that breaks a stream must come with an upcaster, not with an edited fixture.
"""

import json
import unittest
from datetime import UTC, datetime
from pathlib import Path
from typing import Any
from unittest import mock

from slices.payment.domains.payment import (
    Money,
    OperationId,
    OperationKind,
    OperationRequester,
    OperationState,
    Payment,
    PaymentCreated,
    PaymentDomainEvent,
    PaymentId,
    PaymentOperationRequested,
    PaymentState,
    RequesterKind,
)
from slices.payment.infrastructure.repository import UnreadableStoredDataError
from slices.payment.infrastructure.repository.event_codec import (
    _SCHEMAS,
    UPCASTERS,
    decode_event,
    decode_snapshot,
    encode_event,
    encode_snapshot,
)

STREAMS = Path(__file__).parent / "fixtures" / "payment_streams"
CREATED_PAYLOAD = {
    "payment_id": "payment-1",
    "order_id": "order-1",
    "market_id": "berlin",
    "amount": {"amount": "25.00", "currency": "EUR"},
}


def recorded_streams() -> dict[str, dict[str, Any]]:
    return {path.stem: json.loads(path.read_text()) for path in sorted(STREAMS.glob("*.json"))}


def decoded(stream: dict[str, Any]) -> list[PaymentDomainEvent]:
    return [
        decode_event(e["event_type"], e["schema_version"], e["payload"]) for e in stream["events"]
    ]


class RecordedStreamsTest(unittest.TestCase):
    def test_streams_are_recorded(self) -> None:
        self.assertEqual(
            set(recorded_streams()),
            {
                "captured_and_refunded",
                "declined_authorization",
                "cancelled_authorization",
                "mixed_schema_versions",
            },
        )

    def test_ac_1032_recorded_stream_reads_into_the_expected_state(self) -> None:
        for name, stream in recorded_streams().items():
            with self.subTest(name):
                payment = Payment.from_history(None, 0, decoded(stream))

                self.assertEqual(payment.state, PaymentState(stream["state"]))
                self.assertEqual(payment.provider_reference, stream["provider_reference"])
                self.assertEqual(
                    [(str(op.id), op.state) for op in payment.operations],
                    [(op_id, OperationState(state)) for op_id, state in stream["operations"]],
                )

    def test_snapshot_at_every_version_equals_the_full_replay(self) -> None:
        for name, stream in recorded_streams().items():
            events = decoded(stream)
            full = Payment.from_history(None, 0, events)
            for version in range(1, len(events) + 1):
                with self.subTest(name, version=version):
                    at_version = Payment.from_history(None, 0, events[:version])
                    # Через JSON: снапшот проходит тот же путь, что и в базе.
                    state = json.loads(json.dumps(encode_snapshot(at_version.model)))
                    restored = Payment.from_history(
                        decode_snapshot(state), version, events[version:]
                    )

                    self.assertEqual(restored.model, full.model)
                    self.assertEqual(restored.version, full.version)

    def test_every_event_survives_encoding(self) -> None:
        for name, stream in recorded_streams().items():
            for event, raw in zip(decoded(stream), stream["events"], strict=True):
                with self.subTest(name, event=raw["event_type"]):
                    encoded = encode_event(event)
                    self.assertEqual(encoded.event_type, raw["event_type"])
                    self.assertEqual(
                        decode_event(encoded.event_type, encoded.schema_version, encoded.payload),
                        event,
                    )
                    if raw["schema_version"] == encoded.schema_version:
                        self.assertEqual(encoded.payload, raw["payload"])

    def test_amount_keeps_its_exact_scale(self) -> None:
        event = decode_event("PaymentCreated", 1, CREATED_PAYLOAD)
        assert isinstance(event, PaymentCreated)
        self.assertEqual(str(event.amount.amount), "25.00")
        self.assertEqual(event.payment_id, PaymentId("payment-1"))


class VersioningTest(unittest.TestCase):
    def test_ac_1033_unknown_type_or_version_is_an_error(self) -> None:
        with self.assertRaisesRegex(UnreadableStoredDataError, "unknown event type"):
            decode_event("PaymentTeleported", 1, {})
        with self.assertRaisesRegex(UnreadableStoredDataError, "unknown version"):
            decode_event("PaymentCreated", 2, CREATED_PAYLOAD)

    def test_payload_outside_the_schema_is_an_error(self) -> None:
        for payload in (
            {},
            {**CREATED_PAYLOAD, "extra": 1},
            {**CREATED_PAYLOAD, "market_id": "Not A Slug"},
            {**CREATED_PAYLOAD, "amount": {"amount": "x", "currency": "EUR"}},
        ):
            with self.subTest(payload=payload), self.assertRaises(UnreadableStoredDataError):
                decode_event("PaymentCreated", 1, payload)

    def test_requested_operation_of_version_1_has_an_unknown_requester(self) -> None:
        stream = recorded_streams()["mixed_schema_versions"]
        payload = stream["events"][1]["payload"]
        self.assertNotIn("requested_by", payload)

        event = decode_event("PaymentOperationRequested", 1, payload)

        assert isinstance(event, PaymentOperationRequested)
        self.assertIsNone(event.requested_by)
        # Строка не меняется: апкаст живёт только в памяти.
        self.assertNotIn("requested_by", payload)

    def test_mixed_stream_keeps_the_requester_of_new_events(self) -> None:
        payment = Payment.from_history(
            None, 0, decoded(recorded_streams()["mixed_schema_versions"])
        )

        self.assertEqual(
            [op.requested_by for op in payment.operations],
            [None, OperationRequester(RequesterKind.USER, "operator-1", "bff")],
        )

    def test_new_event_is_written_at_the_current_version(self) -> None:
        encoded = encode_event(
            PaymentOperationRequested(
                OperationId("op-1"),
                PaymentId("payment-1"),
                OperationKind.CAPTURE,
                "key-1",
                Money.parse("1", "EUR"),
                datetime(2026, 10, 31, tzinfo=UTC),
                OperationRequester(RequesterKind.SERVICE, "worker"),
            )
        )
        self.assertEqual(encoded.schema_version, 2)
        self.assertEqual(
            encoded.payload["requested_by"], {"kind": "service", "id": "worker", "delegate": None}
        )

    def test_old_version_goes_through_the_upcaster(self) -> None:
        schema = _SCHEMAS["PaymentCreated"]
        newer = type(schema)(schema.event_class, 2, schema.fields)
        old = {key: value for key, value in CREATED_PAYLOAD.items() if key != "market_id"}

        def add_market(payload: dict[str, Any]) -> dict[str, Any]:
            return {**payload, "market_id": "berlin"}

        with mock.patch.dict(_SCHEMAS, {"PaymentCreated": newer}):
            with self.assertRaisesRegex(UnreadableStoredDataError, "no upcaster"):
                decode_event("PaymentCreated", 1, old)
            with mock.patch.dict(UPCASTERS, {("PaymentCreated", 1): add_market}):
                event = decode_event("PaymentCreated", 1, old)

        assert isinstance(event, PaymentCreated)
        self.assertEqual(str(event.market_id), "berlin")

    def test_ac_1041_unreadable_snapshot_is_an_error(self) -> None:
        with self.assertRaises(UnreadableStoredDataError):
            decode_snapshot({"id": "payment-1"})
        with self.assertRaises(UnreadableStoredDataError):
            decode_snapshot({**CREATED_PAYLOAD, "operations": []})


if __name__ == "__main__":
    unittest.main()
