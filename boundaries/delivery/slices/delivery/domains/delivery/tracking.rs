//! Virtual City and the tracking of a Delivery (FR-658, FR-659): the demo
//! space of deliveries is a square grid of streets inside a latitude and
//! longitude frame. Addresses are not part of the project, so a Delivery gets
//! random intersections as its pickup and dropoff points and a random
//! Tracking Channel that carries its live courier positions.

use std::error::Error;
use std::fmt::{Display, Formatter};

use crate::slices::courier::domains::courier::{Location, LocationError};

/// Meters of one degree of latitude; one degree of longitude is this times
/// `cos(latitude)`. Exact enough at the scale of a city.
const METERS_PER_DEGREE: f64 = 111_320.0;

/// Bytes of a Tracking Channel: 128 random bits.
pub const TRACKING_CHANNEL_BYTES: usize = 16;

/// Intersection of the Virtual City grid: `x` counts blocks eastwards, `y`
/// northwards from the south-west corner, both within `0..=blocks`.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub struct Intersection {
    pub x: u32,
    pub y: u32,
}

impl Intersection {
    /// Blocks between two intersections along the streets.
    #[must_use]
    pub const fn blocks_to(self, other: Self) -> u32 {
        self.x.abs_diff(other.x) + self.y.abs_diff(other.y)
    }
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub enum VirtualCityError {
    /// A grid needs at least two blocks per side.
    TooFewBlocks,
    BlockSizeNotPositive,
    /// A corner of the frame is outside latitude and longitude ranges.
    OutOfRange(LocationError),
}

impl Display for VirtualCityError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::TooFewBlocks => formatter.write_str("virtual city needs at least 2 blocks"),
            Self::BlockSizeNotPositive => {
                formatter.write_str("virtual city block size must be a positive number of meters")
            }
            Self::OutOfRange(error) => write!(formatter, "virtual city frame: {error}"),
        }
    }
}

impl Error for VirtualCityError {}

/// Square grid of `blocks × blocks` blocks of `block_meters` from the
/// south-west corner `origin`.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct VirtualCity {
    origin: Location,
    blocks: u32,
    block_meters: f64,
}

impl VirtualCity {
    pub fn new(
        origin_latitude: f64,
        origin_longitude: f64,
        blocks: u32,
        block_meters: f64,
    ) -> Result<Self, VirtualCityError> {
        if blocks < 2 {
            return Err(VirtualCityError::TooFewBlocks);
        }
        if !block_meters.is_finite() || block_meters <= 0.0 {
            return Err(VirtualCityError::BlockSizeNotPositive);
        }
        let origin = Location::new(origin_latitude, origin_longitude)
            .map_err(VirtualCityError::OutOfRange)?;
        let city = Self {
            origin,
            blocks,
            block_meters,
        };
        // Both coordinates grow monotonically with the grid, so every point
        // of a city whose far corner is in range is in range too.
        city.point(f64::from(blocks), f64::from(blocks))
            .map_err(VirtualCityError::OutOfRange)?;
        Ok(city)
    }

    #[must_use]
    pub const fn origin(&self) -> Location {
        self.origin
    }

    #[must_use]
    pub const fn blocks(&self) -> u32 {
        self.blocks
    }

    #[must_use]
    pub const fn block_meters(&self) -> f64 {
        self.block_meters
    }

    /// Number of intersections: `(blocks + 1)²`.
    #[must_use]
    pub const fn intersections(&self) -> u32 {
        (self.blocks + 1) * (self.blocks + 1)
    }

    /// Intersection number `index` (row by row from the south-west corner);
    /// `index` is taken modulo [`Self::intersections`].
    #[must_use]
    pub const fn intersection(&self, index: u32) -> Intersection {
        let index = index % self.intersections();
        Intersection {
            x: index % (self.blocks + 1),
            y: index / (self.blocks + 1),
        }
    }

    /// Coordinates of an intersection of the grid.
    pub fn location(&self, at: Intersection) -> Result<Location, LocationError> {
        self.point(f64::from(at.x), f64::from(at.y))
    }

    /// Coordinates of a point `x` blocks east and `y` blocks north of the
    /// south-west corner; fractions lie between intersections.
    pub fn point(&self, x: f64, y: f64) -> Result<Location, LocationError> {
        let latitude = self.origin.latitude() + y * self.block_meters / METERS_PER_DEGREE;
        let longitude = self.origin.longitude()
            + x * self.block_meters
                / (METERS_PER_DEGREE * self.origin.latitude().to_radians().cos());
        Location::new(latitude, longitude)
    }

    /// Intersection nearest to `location`; a location outside the frame is
    /// pressed to its edge.
    #[must_use]
    pub fn nearest(&self, location: &Location) -> Intersection {
        let y =
            (location.latitude() - self.origin.latitude()) * METERS_PER_DEGREE / self.block_meters;
        let x = (location.longitude() - self.origin.longitude())
            * METERS_PER_DEGREE
            * self.origin.latitude().to_radians().cos()
            / self.block_meters;
        Intersection {
            x: self.clamp(x),
            y: self.clamp(y),
        }
    }

    fn clamp(&self, blocks: f64) -> u32 {
        let rounded = blocks.round().clamp(0.0, f64::from(self.blocks));
        // Within `0..=blocks` after the clamp.
        #[allow(clippy::cast_possible_truncation, clippy::cast_sign_loss)]
        let value = rounded as u32;
        value
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct TrackingChannelError;

impl Display for TrackingChannelError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        formatter.write_str("tracking channel must be 32 lowercase hexadecimal digits")
    }
}

impl Error for TrackingChannelError {}

/// Capability of the live position stream of a Delivery: 128 random bits as
/// 32 lowercase hexadecimal digits. Whoever knows it may subscribe to the
/// stream, so only Get Order Tracking hands it out (FR-660).
#[derive(Clone, Debug, Eq, Hash, PartialEq)]
pub struct TrackingChannel(String);

impl TrackingChannel {
    #[must_use]
    pub fn from_bytes(bytes: [u8; TRACKING_CHANNEL_BYTES]) -> Self {
        Self(bytes.iter().map(|byte| format!("{byte:02x}")).collect())
    }

    /// A stored channel.
    pub fn restore(value: impl Into<String>) -> Result<Self, TrackingChannelError> {
        let value = value.into();
        let valid = value.len() == TRACKING_CHANNEL_BYTES * 2
            && value
                .bytes()
                .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte));
        if valid {
            Ok(Self(value))
        } else {
            Err(TrackingChannelError)
        }
    }

    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

/// Pickup and dropoff points of a Delivery in the Virtual City and its
/// Tracking Channel; set on creation, immutable.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct DeliveryTracking {
    pub pickup: Intersection,
    pub dropoff: Intersection,
    pub channel: TrackingChannel,
}

#[cfg(test)]
mod tests {
    use super::*;

    fn city() -> VirtualCity {
        VirtualCity::new(52.515, 13.385, 12, 150.0).expect("city")
    }

    /// AC-870
    #[test]
    fn intersection_maps_to_coordinates() {
        let location = city()
            .location(Intersection { x: 0, y: 2 })
            .expect("location");

        assert!((location.longitude() - 13.385).abs() < 1e-12);
        assert!((location.latitude() - (52.515 + 300.0 / 111_320.0)).abs() < 1e-12);
    }

    #[test]
    fn nearest_intersection_is_found_and_pressed_to_the_edge() {
        let city = city();
        let inside = city
            .location(Intersection { x: 3, y: 7 })
            .expect("location");
        assert_eq!(city.nearest(&inside), Intersection { x: 3, y: 7 });

        let outside = Location::new(60.0, 0.0).expect("location");
        assert_eq!(city.nearest(&outside), Intersection { x: 0, y: 12 });
    }

    #[test]
    fn intersections_are_numbered_row_by_row() {
        let city = city();
        assert_eq!(city.intersections(), 169);
        assert_eq!(city.intersection(0), Intersection { x: 0, y: 0 });
        assert_eq!(city.intersection(14), Intersection { x: 1, y: 1 });
        assert_eq!(city.intersection(169), Intersection { x: 0, y: 0 });
    }

    /// AC-871 (the domain part; configuration loading fails on it).
    #[test]
    fn invalid_grids_are_rejected() {
        assert_eq!(
            VirtualCity::new(52.5, 13.4, 1, 150.0),
            Err(VirtualCityError::TooFewBlocks)
        );
        assert_eq!(
            VirtualCity::new(52.5, 13.4, 12, 0.0),
            Err(VirtualCityError::BlockSizeNotPositive)
        );
        assert_eq!(
            VirtualCity::new(89.999, 13.4, 12, 150.0),
            Err(VirtualCityError::OutOfRange(
                LocationError::LatitudeOutOfRange
            ))
        );
    }

    #[test]
    fn blocks_between_intersections_follow_the_streets() {
        assert_eq!(
            Intersection { x: 1, y: 5 }.blocks_to(Intersection { x: 4, y: 2 }),
            6
        );
    }

    #[test]
    fn channel_is_lowercase_hex_of_128_bits() {
        let channel = TrackingChannel::from_bytes([0xab; TRACKING_CHANNEL_BYTES]);
        assert_eq!(channel.as_str(), "ab".repeat(16));
        assert_eq!(TrackingChannel::restore(channel.as_str()), Ok(channel));
        assert_eq!(
            TrackingChannel::restore("AB".repeat(16)),
            Err(TrackingChannelError)
        );
        assert_eq!(TrackingChannel::restore("ab"), Err(TrackingChannelError));
    }
}
