use std::sync::{Mutex, PoisonError};

use crate::slices::delivery::applications::tracking::{RandomSource, RandomSourceError};

/// Test random source: hands out the given bytes in order and starts over at
/// the end, so tests get the points and channels they expect. Predictable,
/// so it is not a production implementation.
#[derive(Debug)]
pub struct FixedRandomSource {
    bytes: Vec<u8>,
    next: Mutex<usize>,
}

impl FixedRandomSource {
    /// `bytes` must not be empty.
    #[must_use]
    pub fn new(bytes: Vec<u8>) -> Self {
        Self {
            bytes,
            next: Mutex::new(0),
        }
    }

    /// Every byte is `byte`.
    #[must_use]
    pub fn repeating(byte: u8) -> Self {
        Self::new(vec![byte])
    }
}

impl RandomSource for FixedRandomSource {
    fn fill(&self, bytes: &mut [u8]) -> Result<(), RandomSourceError> {
        let mut next = self.next.lock().unwrap_or_else(PoisonError::into_inner);
        for byte in bytes {
            *byte = self.bytes[*next % self.bytes.len()];
            *next += 1;
        }
        Ok(())
    }
}
