use std::error::Error;
use std::fmt::{Display, Formatter};

/// Longest courier name shown on the flow board and to the customer.
pub const COURIER_NAME_MAX_CHARS: usize = 64;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum CourierNameError {
    Empty,
    TooLong,
}

impl Display for CourierNameError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Empty => formatter.write_str("courier name must not be empty"),
            Self::TooLong => write!(
                formatter,
                "courier name must not be longer than {COURIER_NAME_MAX_CHARS} characters"
            ),
        }
    }
}

impl Error for CourierNameError {}

/// Name of a courier of the Courier Pool: trimmed, 1..=64 characters.
#[derive(Clone, Debug, Eq, Hash, PartialEq)]
pub struct CourierName(String);

impl CourierName {
    pub fn new(value: &str) -> Result<Self, CourierNameError> {
        let trimmed = value.trim();
        if trimmed.is_empty() {
            return Err(CourierNameError::Empty);
        }
        if trimmed.chars().count() > COURIER_NAME_MAX_CHARS {
            return Err(CourierNameError::TooLong);
        }
        Ok(Self(trimmed.to_owned()))
    }

    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// AC-787
    #[test]
    fn name_is_trimmed_and_bounded() {
        assert_eq!(
            CourierName::new("  Тимур ").map(|name| name.as_str().to_owned()),
            Ok("Тимур".to_owned())
        );
        assert_eq!(CourierName::new("   "), Err(CourierNameError::Empty));
        assert!(CourierName::new(&"я".repeat(COURIER_NAME_MAX_CHARS)).is_ok());
        assert_eq!(
            CourierName::new(&"я".repeat(COURIER_NAME_MAX_CHARS + 1)),
            Err(CourierNameError::TooLong)
        );
    }
}
