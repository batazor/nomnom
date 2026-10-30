//! Schema of a record of the stream (FR-714, FR-715): the value is in the
//! Schema Registry wire format — magic byte 0, the schema id (4 bytes
//! big-endian), the message indexes (a zigzag varint array, the single byte 0
//! for the first message of the file) and the Protobuf bytes of the contract.
//! The subject of an Integration Event is its `event_type` (FR-717).

use std::collections::HashMap;
use std::sync::{Arc, Mutex, PoisonError};
use std::time::Duration;

use serde::Deserialize;
use url::Url;

/// Deadline of one request to the Registry.
const REQUEST_TIMEOUT: Duration = Duration::from_secs(5);

/// Schema id and Protobuf payload of a record value.
#[derive(Debug, PartialEq, Eq)]
pub struct WireFrame<'a> {
    pub schema_id: u32,
    pub payload: &'a [u8],
}

/// Splits the value; an error is permanent: the value is not in the wire format.
pub fn parse_wire_frame(value: &[u8]) -> Result<WireFrame<'_>, String> {
    let malformed =
        |reason: &str| format!("value is not in the schema registry wire format: {reason}");
    if value.len() < 6 {
        return Err(malformed(&format!("{} bytes", value.len())));
    }
    if value[0] != 0 {
        return Err(malformed(&format!("starts with {:#x}", value[0])));
    }
    let schema_id = u32::from_be_bytes([value[1], value[2], value[3], value[4]]);
    let (count, mut rest) =
        read_varint(&value[5..]).ok_or_else(|| malformed("message indexes are truncated"))?;
    if count < 0 {
        return Err(malformed("negative message index count"));
    }
    for _ in 0..count {
        let (index, next) =
            read_varint(rest).ok_or_else(|| malformed("message indexes are truncated"))?;
        if index < 0 {
            return Err(malformed("negative message index"));
        }
        rest = next;
    }
    Ok(WireFrame {
        schema_id,
        payload: rest,
    })
}

/// Zigzag varint, as `binary.Varint` of Go and `ByteUtils.readVarint` of Kafka.
fn read_varint(bytes: &[u8]) -> Option<(i64, &[u8])> {
    let mut raw: u64 = 0;
    for (at, byte) in bytes.iter().enumerate().take(10) {
        raw |= u64::from(byte & 0x7f) << (7 * at);
        if byte & 0x80 == 0 {
            let value = (raw >> 1) as i64 ^ -((raw & 1) as i64);
            return Some((value, &bytes[at + 1..]));
        }
    }
    None
}

/// Failure to resolve the subjects of a schema id.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum SchemaLookupError {
    /// The Registry does not know the id: no redelivery reads the record.
    Unknown(u32),
    /// The Registry is unavailable; the lookup is repeated.
    Unavailable(String),
}

/// Subjects of schema ids from the Confluent-compatible Schema Registry. Ids
/// are immutable, so a resolved id is kept for the life of the process.
pub struct SchemaRegistry {
    base: Url,
    client: reqwest::Client,
    known: Mutex<HashMap<u32, Arc<[String]>>>,
}

#[derive(Deserialize)]
struct SubjectVersion {
    subject: String,
}

impl SchemaRegistry {
    /// `url` — absolute http(s) URL of the Registry.
    pub fn new(url: &str) -> Result<Self, String> {
        let mut base =
            Url::parse(url).map_err(|error| format!("schema registry url {url:?}: {error}"))?;
        if !matches!(base.scheme(), "http" | "https") || base.host().is_none() {
            return Err(format!(
                "schema registry url {url:?} must be an absolute http(s) url"
            ));
        }
        if !base.path().ends_with('/') {
            let path = format!("{}/", base.path());
            base.set_path(&path);
        }
        let tls = rustls::ClientConfig::builder_with_provider(Arc::new(
            rustls::crypto::ring::default_provider(),
        ))
        .with_safe_default_protocol_versions()
        .map_err(|error| format!("schema registry tls: {error}"))?
        .with_root_certificates(rustls::RootCertStore {
            roots: webpki_roots::TLS_SERVER_ROOTS.to_vec(),
        })
        .with_no_client_auth();
        let client = reqwest::Client::builder()
            .tls_backend_preconfigured(tls)
            .timeout(REQUEST_TIMEOUT)
            .build()
            .map_err(|error| format!("schema registry client: {error}"))?;
        Ok(Self {
            base,
            client,
            known: Mutex::new(HashMap::new()),
        })
    }

    pub async fn subjects(&self, schema_id: u32) -> Result<Arc<[String]>, SchemaLookupError> {
        if let Some(subjects) = self
            .known
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .get(&schema_id)
        {
            return Ok(Arc::clone(subjects));
        }
        let url = self
            .base
            .join(&format!("schemas/ids/{schema_id}/versions"))
            .map_err(|error| SchemaLookupError::Unavailable(error.to_string()))?;
        let unavailable = |error: reqwest::Error| {
            SchemaLookupError::Unavailable(format!("schema registry: {error}"))
        };
        let response = self.client.get(url).send().await.map_err(unavailable)?;
        match response.status().as_u16() {
            200 => {}
            404 => return Err(SchemaLookupError::Unknown(schema_id)),
            status => {
                let body = response.text().await.unwrap_or_default();
                return Err(SchemaLookupError::Unavailable(format!(
                    "schema registry: subjects of schema {schema_id}: status {status}: {body}"
                )));
            }
        }
        let body = response.bytes().await.map_err(unavailable)?;
        let versions: Vec<SubjectVersion> = serde_json::from_slice(&body).map_err(|error| {
            SchemaLookupError::Unavailable(format!(
                "decode subjects of schema {schema_id}: {error}"
            ))
        })?;
        let subjects: Arc<[String]> = versions
            .into_iter()
            .map(|version| version.subject)
            .collect();
        self.known
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .insert(schema_id, Arc::clone(&subjects));
        Ok(subjects)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn splits_schema_id_from_payload() {
        // Second message of the file: indexes [1] → count 1 (0x02), index 1 (0x02).
        let value = [0, 0, 0, 0, 9, 0x02, 0x02, 0x0a, 0x01, b'o'];
        assert_eq!(
            parse_wire_frame(&value),
            Ok(WireFrame {
                schema_id: 9,
                payload: &[0x0a, 0x01, b'o'],
            })
        );
        // First message of the file: the single byte 0.
        assert_eq!(
            parse_wire_frame(&[0, 0, 0, 1, 0, 0, 7]).map(|frame| frame.payload),
            Ok(&[7][..])
        );
    }

    /// AC-977: a value outside the wire format is a permanent error.
    #[test]
    fn refuses_value_outside_wire_format() {
        for value in [
            &b"\x0a\x01o"[..],
            &[1, 0, 0, 0, 9, 0, 7][..],
            &[0, 0, 0, 0, 9, 0x02][..],
            &[0, 0, 0, 0, 9, 0x02, 0x01][..],
            &[0, 0, 0, 9][..],
        ] {
            assert!(parse_wire_frame(value).is_err(), "{value:?}");
        }
    }

    #[test]
    fn refuses_registry_url_that_is_not_http() {
        assert!(SchemaRegistry::new("kafka:8081").is_err());
        assert!(SchemaRegistry::new("ftp://kafka:8081").is_err());
        assert!(SchemaRegistry::new("http://kafka:8081").is_ok());
    }
}
