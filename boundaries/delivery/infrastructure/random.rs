//! Случайность точек и Tracking Channel новых доставок в production-сборке
//! (FR-659): генератор операционной системы. Канал — capability потока
//! позиций, поэтому он должен быть непредсказуемым. Фиксированный источник
//! `infrastructure::memory` — только для тестов.

use crate::slices::delivery::applications::tracking::{RandomSource, RandomSourceError};

#[derive(Clone, Copy, Debug, Default)]
pub struct SystemRandomSource;

impl RandomSource for SystemRandomSource {
    fn fill(&self, bytes: &mut [u8]) -> Result<(), RandomSourceError> {
        getrandom::fill(bytes).map_err(|error| RandomSourceError(error.to_string()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fills_different_bytes_on_each_call() {
        let mut first = [0_u8; 16];
        let mut second = [0_u8; 16];
        SystemRandomSource.fill(&mut first).expect("random");
        SystemRandomSource.fill(&mut second).expect("random");

        assert_ne!(first, second);
    }
}
