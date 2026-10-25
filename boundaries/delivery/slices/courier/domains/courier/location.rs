use std::error::Error;
use std::fmt::{Display, Formatter};

const MAXIMUM_LATITUDE: f64 = 90.0;
const MAXIMUM_LONGITUDE: f64 = 180.0;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum LocationError {
    LatitudeOutOfRange,
    LongitudeOutOfRange,
}

impl Display for LocationError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::LatitudeOutOfRange => {
                formatter.write_str("latitude must be a finite value within [-90, 90]")
            }
            Self::LongitudeOutOfRange => {
                formatter.write_str("longitude must be a finite value within [-180, 180]")
            }
        }
    }
}

impl Error for LocationError {}

/// Last known geographic position of a courier in WGS 84 degrees.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Location {
    latitude: f64,
    longitude: f64,
}

impl Location {
    pub fn new(latitude: f64, longitude: f64) -> Result<Self, LocationError> {
        if !latitude.is_finite() || latitude.abs() > MAXIMUM_LATITUDE {
            return Err(LocationError::LatitudeOutOfRange);
        }
        if !longitude.is_finite() || longitude.abs() > MAXIMUM_LONGITUDE {
            return Err(LocationError::LongitudeOutOfRange);
        }
        Ok(Self {
            latitude,
            longitude,
        })
    }

    #[must_use]
    pub const fn latitude(&self) -> f64 {
        self.latitude
    }

    #[must_use]
    pub const fn longitude(&self) -> f64 {
        self.longitude
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn location_accepts_boundary_coordinates() {
        let location = Location::new(-90.0, 180.0).expect("boundary coordinates");
        assert!((location.latitude() + 90.0).abs() < f64::EPSILON);
        assert!((location.longitude() - 180.0).abs() < f64::EPSILON);
    }

    #[test]
    fn location_rejects_out_of_range_and_non_finite_coordinates() {
        assert_eq!(
            Location::new(90.1, 0.0),
            Err(LocationError::LatitudeOutOfRange)
        );
        assert_eq!(
            Location::new(f64::NAN, 0.0),
            Err(LocationError::LatitudeOutOfRange)
        );
        assert_eq!(
            Location::new(0.0, -180.1),
            Err(LocationError::LongitudeOutOfRange)
        );
        assert_eq!(
            Location::new(0.0, f64::INFINITY),
            Err(LocationError::LongitudeOutOfRange)
        );
    }
}
