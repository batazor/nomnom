//! Event Envelope of an incoming message (ADR 31): the Kafka headers the
//! Outbox of the publisher writes. `event_id`, `event_type` and
//! `occurred_at` (RFC 3339, UTC) are required; a missing or malformed one
//! makes the message a dead letter.

use std::error::Error;
use std::fmt::{Display, Formatter};
use std::time::UNIX_EPOCH;

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Envelope {
    pub event_id: String,
    /// Full Protobuf message name of the payload.
    pub event_type: String,
    /// `occurred_at`, epoch milliseconds.
    pub occurred_at_ms: i64,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum EnvelopeError {
    Missing(&'static str),
    NotUtf8(&'static str),
    OccurredAt(String),
}

impl Display for EnvelopeError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Missing(header) => write!(formatter, "envelope header {header} is missing"),
            Self::NotUtf8(header) => write!(formatter, "envelope header {header} is not UTF-8"),
            Self::OccurredAt(value) => {
                write!(
                    formatter,
                    "envelope occurred_at \"{value}\" is not RFC 3339 UTC"
                )
            }
        }
    }
}

impl Error for EnvelopeError {}

impl Envelope {
    /// Envelope of the headers of a message.
    pub fn parse(headers: &[(String, Vec<u8>)]) -> Result<Self, EnvelopeError> {
        let required = |name: &'static str| -> Result<String, EnvelopeError> {
            let value = headers
                .iter()
                .find(|(key, _)| key == name)
                .map(|(_, value)| value)
                .ok_or(EnvelopeError::Missing(name))?;
            let value = std::str::from_utf8(value).map_err(|_| EnvelopeError::NotUtf8(name))?;
            let value = value.trim();
            if value.is_empty() {
                return Err(EnvelopeError::Missing(name));
            }
            Ok(value.to_owned())
        };
        let event_id = required("event_id")?;
        let event_type = required("event_type")?;
        let occurred_at = required("occurred_at")?;
        let occurred_at_ms = humantime::parse_rfc3339(&occurred_at)
            .ok()
            .and_then(|at| at.duration_since(UNIX_EPOCH).ok())
            .and_then(|since| i64::try_from(since.as_millis()).ok())
            .ok_or(EnvelopeError::OccurredAt(occurred_at))?;
        Ok(Self {
            event_id,
            event_type,
            occurred_at_ms,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn headers(pairs: &[(&str, &str)]) -> Vec<(String, Vec<u8>)> {
        pairs
            .iter()
            .map(|(key, value)| ((*key).to_owned(), value.as_bytes().to_vec()))
            .collect()
    }

    #[test]
    fn parses_the_envelope_of_the_outbox() {
        let envelope = Envelope::parse(&headers(&[
            ("event_id", "0b5e2a4c-0000-5000-8000-000000000001"),
            ("event_type", "nomnom.delivery.v1.DeliveryPickedUp"),
            ("occurred_at", "2026-10-28T10:15:30.250123Z"),
            ("aggregate_id", "delivery-1"),
        ]))
        .expect("envelope");

        assert_eq!(
            envelope,
            Envelope {
                event_id: "0b5e2a4c-0000-5000-8000-000000000001".to_owned(),
                event_type: "nomnom.delivery.v1.DeliveryPickedUp".to_owned(),
                occurred_at_ms: 1_793_182_530_250,
            }
        );
    }

    #[test]
    fn missing_or_malformed_headers_are_rejected() {
        assert_eq!(
            Envelope::parse(&headers(&[
                ("event_type", "nomnom.delivery.v1.DeliveryPickedUp"),
                ("occurred_at", "2026-10-28T10:15:30Z"),
            ])),
            Err(EnvelopeError::Missing("event_id"))
        );
        assert_eq!(
            Envelope::parse(&headers(&[
                ("event_id", "e-1"),
                ("event_type", "nomnom.delivery.v1.DeliveryPickedUp"),
                ("occurred_at", "yesterday"),
            ])),
            Err(EnvelopeError::OccurredAt("yesterday".to_owned()))
        );
    }
}
