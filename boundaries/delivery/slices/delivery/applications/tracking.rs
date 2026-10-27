//! Tracking of a new Delivery (FR-659): two different random intersections
//! of the Virtual City as pickup and dropoff points and a random Tracking
//! Channel.

use std::error::Error;
use std::fmt::{Display, Formatter};

use crate::slices::delivery::domains::delivery::{
    DeliveryTracking, TRACKING_CHANNEL_BYTES, TrackingChannel, VirtualCity,
};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RandomSourceError(pub String);

impl Display for RandomSourceError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        write!(formatter, "random source unavailable: {}", self.0)
    }
}

impl Error for RandomSourceError {}

/// Random bytes: the operating system in the process, a fixed sequence in
/// tests. A Tracking Channel is a capability, so the process source must be
/// unpredictable.
pub trait RandomSource {
    fn fill(&self, bytes: &mut [u8]) -> Result<(), RandomSourceError>;
}

/// Bytes read at once: two intersection indices and the channel.
const TRACKING_BYTES: usize = 8 + TRACKING_CHANNEL_BYTES;

/// Pickup is a random intersection; dropoff is one of the others, chosen
/// without retries by a random offset from the pickup.
pub fn new_tracking(
    city: &VirtualCity,
    random: &dyn RandomSource,
) -> Result<DeliveryTracking, RandomSourceError> {
    let mut bytes = [0_u8; TRACKING_BYTES];
    random.fill(&mut bytes)?;
    let (pickup_bytes, rest) = bytes.split_at(4);
    let (dropoff_bytes, channel_bytes) = rest.split_at(4);
    let total = city.intersections();
    let pickup = word(pickup_bytes) % total;
    let dropoff = pickup + 1 + word(dropoff_bytes) % (total - 1);
    let mut channel = [0_u8; TRACKING_CHANNEL_BYTES];
    channel.copy_from_slice(channel_bytes);
    Ok(DeliveryTracking {
        pickup: city.intersection(pickup),
        dropoff: city.intersection(dropoff),
        channel: TrackingChannel::from_bytes(channel),
    })
}

fn word(bytes: &[u8]) -> u32 {
    bytes
        .iter()
        .fold(0, |word, byte| (word << 8) | u32::from(*byte))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::FixedRandomSource;
    use crate::slices::delivery::domains::delivery::Intersection;

    fn city() -> VirtualCity {
        VirtualCity::new(52.515, 13.385, 2, 150.0).expect("city")
    }

    #[test]
    fn pickup_and_dropoff_are_different_intersections() {
        for seed in 0..=u8::MAX {
            let tracking =
                new_tracking(&city(), &FixedRandomSource::repeating(seed)).expect("tracking");
            assert_ne!(tracking.pickup, tracking.dropoff, "seed {seed}");
        }
    }

    #[test]
    fn points_and_channel_come_from_the_random_bytes() {
        // Pickup index 4 → (1, 1); dropoff offset 1 → index 6 → (0, 2).
        let mut bytes = vec![0, 0, 0, 4, 0, 0, 0, 1];
        bytes.extend([0xcd; TRACKING_CHANNEL_BYTES]);
        let tracking = new_tracking(&city(), &FixedRandomSource::new(bytes)).expect("tracking");

        assert_eq!(tracking.pickup, Intersection { x: 1, y: 1 });
        assert_eq!(tracking.dropoff, Intersection { x: 0, y: 2 });
        assert_eq!(tracking.channel.as_str(), "cd".repeat(16));
    }
}
