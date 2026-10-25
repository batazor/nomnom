//! Kafka headers of an Integration Event. The format matches
//! `orders/pkg/messaging/envelope.go` (`NewIntegrationEventMessage`) and the
//! Watermill Kafka marshaler, so consumers do not depend on the publisher's
//! language.

use super::relay::OutboxEntry;

/// Watermill message UUID header; `orders` consumers read it as `msg.UUID`.
pub const WATERMILL_UUID_HEADER: &str = "_watermill_message_uuid";

/// `(name, value)` pairs in publication order.
pub type EnvelopeHeaders = Vec<(&'static str, String)>;

/// Event Envelope headers of an Outbox row. `traceparent` is sent when the
/// row carries one.
#[must_use]
pub fn headers(entry: &OutboxEntry) -> EnvelopeHeaders {
    let mut headers = vec![
        (WATERMILL_UUID_HEADER, entry.event_id.clone()),
        ("event_id", entry.event_id.clone()),
        ("event_type", entry.event_type.clone()),
        ("occurred_at", format_occurred_at(&entry.occurred_at)),
        ("aggregate_type", entry.aggregate_type.clone()),
        ("aggregate_id", entry.aggregate_id.clone()),
        ("aggregate_version", entry.aggregate_version.to_string()),
    ];
    if let Some(traceparent) = entry
        .traceparent
        .as_deref()
        .filter(|value| !value.is_empty())
    {
        headers.push(("traceparent", traceparent.to_owned()));
    }
    headers
}

/// Go `time.RFC3339Nano` of a UTC timestamp `YYYY-MM-DDTHH:MM:SS[.ffffff]`:
/// trailing zeros of the fraction are trimmed, a zero fraction is omitted,
/// the zone is `Z`.
#[must_use]
pub fn format_occurred_at(utc: &str) -> String {
    let (seconds, fraction) = utc.split_once('.').unwrap_or((utc, ""));
    let fraction = fraction.trim_end_matches('0');
    if fraction.is_empty() {
        format!("{seconds}Z")
    } else {
        format!("{seconds}.{fraction}Z")
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn entry(traceparent: Option<&str>) -> OutboxEntry {
        OutboxEntry {
            seq: 7,
            event_id: "4f6b0f8e-3c2a-5d1e-9a0b-1c2d3e4f5a6b".to_owned(),
            event_type: "nomnom.delivery.v1.DeliveryPickedUp".to_owned(),
            aggregate_type: "delivery".to_owned(),
            aggregate_id: "delivery-1".to_owned(),
            aggregate_version: 3,
            occurred_at: "2026-10-25T10:15:30.120000".to_owned(),
            occurred_at_epoch: 0.0,
            topic: "delivery.v1.delivery-events".to_owned(),
            payload: Vec::new(),
            traceparent: traceparent.map(str::to_owned),
        }
    }

    #[test]
    fn headers_match_orders_envelope() {
        let traceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01";
        assert_eq!(
            headers(&entry(Some(traceparent))),
            vec![
                (
                    WATERMILL_UUID_HEADER,
                    "4f6b0f8e-3c2a-5d1e-9a0b-1c2d3e4f5a6b".to_owned()
                ),
                (
                    "event_id",
                    "4f6b0f8e-3c2a-5d1e-9a0b-1c2d3e4f5a6b".to_owned()
                ),
                (
                    "event_type",
                    "nomnom.delivery.v1.DeliveryPickedUp".to_owned()
                ),
                ("occurred_at", "2026-10-25T10:15:30.12Z".to_owned()),
                ("aggregate_type", "delivery".to_owned()),
                ("aggregate_id", "delivery-1".to_owned()),
                ("aggregate_version", "3".to_owned()),
                ("traceparent", traceparent.to_owned()),
            ]
        );
    }

    #[test]
    fn traceparent_is_optional() {
        assert!(
            headers(&entry(None))
                .iter()
                .all(|(name, _)| *name != "traceparent")
        );
        assert!(
            headers(&entry(Some("")))
                .iter()
                .all(|(name, _)| *name != "traceparent")
        );
    }

    #[test]
    fn occurred_at_is_rfc3339_nano_utc() {
        assert_eq!(
            format_occurred_at("2026-10-25T10:15:30.123456"),
            "2026-10-25T10:15:30.123456Z"
        );
        assert_eq!(
            format_occurred_at("2026-10-25T10:15:30.100000"),
            "2026-10-25T10:15:30.1Z"
        );
        assert_eq!(
            format_occurred_at("2026-10-25T10:15:30.000000"),
            "2026-10-25T10:15:30Z"
        );
        assert_eq!(
            format_occurred_at("2026-10-25T10:15:30"),
            "2026-10-25T10:15:30Z"
        );
    }
}
