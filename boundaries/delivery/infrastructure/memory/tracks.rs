use std::sync::{Mutex, PoisonError};

use crate::pkg::persistence::RepositoryError;
use crate::slices::courier::applications::update_location::{
    CourierPosition, CourierPositionPublisher,
};

/// Test publisher of courier positions: records every publication and can
/// simulate a Valkey outage. It publishes nowhere, so it is not a production
/// implementation.
#[derive(Debug, Default)]
pub struct RecordingPositionPublisher {
    published: Mutex<Vec<(String, CourierPosition)>>,
    failure: Mutex<Option<String>>,
}

impl RecordingPositionPublisher {
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// Every publication fails with `reason`.
    #[must_use]
    pub fn failing(reason: &str) -> Self {
        let publisher = Self::new();
        *publisher
            .failure
            .lock()
            .unwrap_or_else(PoisonError::into_inner) = Some(reason.to_owned());
        publisher
    }

    /// Successful publications as `(channel, position)` in call order.
    #[must_use]
    pub fn published(&self) -> Vec<(String, CourierPosition)> {
        self.published
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .clone()
    }
}

impl CourierPositionPublisher for RecordingPositionPublisher {
    fn publish(&self, channel: &str, position: &CourierPosition) -> Result<(), RepositoryError> {
        if let Some(reason) = self
            .failure
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .clone()
        {
            return Err(RepositoryError::Unavailable(reason));
        }
        self.published
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .push((channel.to_owned(), position.clone()));
        Ok(())
    }
}
