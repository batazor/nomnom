//! Поток позиций курьеров (FR-661): Valkey pub/sub. Update Courier Location
//! публикует каждую позицию курьера с активной доставкой в канал
//! `delivery.tracks.<tracking_channel>` JSON-объектом `CourierPosition` с
//! `__typename`: его без изменений отдаёт подписчикам Cosmo Router (ADR 36).
//! Это не кеш ADR 43: Valkey ничего не хранит. Записывающий publisher
//! `infrastructure::memory` — только для тестов.

use std::sync::{Mutex, PoisonError};
use std::time::{Duration, UNIX_EPOCH};

use redis::{Client, Connection};
use serde_json::json;

use crate::infrastructure::repository::blocking;
use crate::pkg::persistence::RepositoryError;
use crate::pkg::telemetry::log::rfc3339;
use crate::slices::courier::applications::update_courier_location::{
    CourierPosition, CourierPositionPublisher,
};

/// Префикс каналов Valkey; Router подписывается на
/// `delivery.tracks.{{ args.channel }}`.
pub const CHANNEL_PREFIX: &str = "delivery.tracks.";

/// Deadline соединения и одной команды: точка ценна только сейчас.
const TIMEOUT: Duration = Duration::from_secs(1);

/// Канал Valkey Tracking Channel.
#[must_use]
pub fn channel_name(channel: &str) -> String {
    format!("{CHANNEL_PREFIX}{channel}")
}

/// Сообщение позиции: поля `CourierPosition` customer-схемы.
#[must_use]
pub fn message(channel: &str, position: &CourierPosition) -> String {
    let recorded_at = UNIX_EPOCH
        + Duration::from_secs(u64::try_from(position.recorded_at_epoch_seconds).unwrap_or(0));
    json!({
        "__typename": "CourierPosition",
        "channel": channel,
        "latitude": position.location.latitude(),
        "longitude": position.location.longitude(),
        "recordedAt": rfc3339(recorded_at),
    })
    .to_string()
}

/// Publisher над одним соединением Valkey. Оборванное соединение
/// сбрасывается, следующая публикация открывает новое.
pub struct ValkeyPositionPublisher {
    client: Client,
    connection: Mutex<Option<Connection>>,
}

impl ValkeyPositionPublisher {
    /// Открывает соединение и проверяет его `PING`: недоступный Valkey
    /// прерывает startup.
    pub fn connect(url: &str) -> Result<Self, RepositoryError> {
        let client = Client::open(url).map_err(unavailable)?;
        let publisher = Self {
            client,
            connection: Mutex::new(None),
        };
        blocking(|| {
            let mut connection = publisher.open()?;
            redis::cmd("PING")
                .query::<String>(&mut connection)
                .map_err(unavailable)?;
            *publisher.lock() = Some(connection);
            Ok::<(), RepositoryError>(())
        })?;
        Ok(publisher)
    }

    fn open(&self) -> Result<Connection, RepositoryError> {
        let connection = self
            .client
            .get_connection_with_timeout(TIMEOUT)
            .map_err(unavailable)?;
        connection
            .set_read_timeout(Some(TIMEOUT))
            .map_err(unavailable)?;
        connection
            .set_write_timeout(Some(TIMEOUT))
            .map_err(unavailable)?;
        Ok(connection)
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, Option<Connection>> {
        self.connection
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
    }
}

impl CourierPositionPublisher for ValkeyPositionPublisher {
    fn publish(&self, channel: &str, position: &CourierPosition) -> Result<(), RepositoryError> {
        let payload = message(channel, position);
        blocking(|| {
            let mut slot = self.lock();
            let mut connection = match slot.take() {
                Some(connection) => connection,
                None => self.open()?,
            };
            redis::cmd("PUBLISH")
                .arg(channel_name(channel))
                .arg(&payload)
                .query::<i64>(&mut connection)
                .map_err(unavailable)?;
            *slot = Some(connection);
            Ok(())
        })
    }
}

fn unavailable(error: impl std::fmt::Display) -> RepositoryError {
    RepositoryError::Unavailable(format!("valkey: {error}"))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::slices::courier::domains::courier::{CourierId, Location};

    #[test]
    fn message_carries_the_fields_of_courier_position() {
        let position = CourierPosition {
            courier_id: CourierId::new("courier-1"),
            location: Location::new(52.52, 13.4).expect("location"),
            recorded_at_epoch_seconds: 1_793_100_000,
        };

        let message: serde_json::Value =
            serde_json::from_str(&message("abc", &position)).expect("json");

        assert_eq!(
            message,
            json!({
                "__typename": "CourierPosition",
                "channel": "abc",
                "latitude": 52.52,
                "longitude": 13.4,
                "recordedAt": "2026-10-27T11:20:00.000Z",
            })
        );
        assert_eq!(channel_name("abc"), "delivery.tracks.abc");
    }
}
