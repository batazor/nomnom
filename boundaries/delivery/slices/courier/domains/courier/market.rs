//! Market (ADR 41): the city whose restaurants, couriers and orders are served
//! together. A Courier works in exactly one Market, a Delivery belongs to the
//! Market of its Order; both are set at creation and never change.

use std::error::Error;
use std::fmt::{Display, Formatter};

/// Shortest and longest `market_id`: `^[a-z][a-z0-9-]{1,31}$`.
pub const MARKET_ID_MIN_CHARS: usize = 2;
pub const MARKET_ID_MAX_CHARS: usize = 32;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum MarketIdError {
    Empty,
    /// Not a slug `^[a-z][a-z0-9-]{1,31}$`.
    InvalidFormat,
}

impl Display for MarketIdError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Empty => formatter.write_str("market_id must not be empty"),
            Self::InvalidFormat => {
                formatter.write_str("market_id must match ^[a-z][a-z0-9-]{1,31}$")
            }
        }
    }
}

impl Error for MarketIdError {}

/// Identifier of a Market: a slug `^[a-z][a-z0-9-]{1,31}$` (`berlin`). The
/// value is taken as given: no trimming or case folding.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub struct MarketId(String);

impl MarketId {
    pub fn new(value: &str) -> Result<Self, MarketIdError> {
        if value.is_empty() {
            return Err(MarketIdError::Empty);
        }
        let mut chars = value.chars();
        let starts_with_letter = chars.next().is_some_and(|first| first.is_ascii_lowercase());
        let slug =
            chars.all(|next| next.is_ascii_lowercase() || next.is_ascii_digit() || next == '-');
        let length = value.len();
        if !starts_with_letter
            || !slug
            || !(MARKET_ID_MIN_CHARS..=MARKET_ID_MAX_CHARS).contains(&length)
        {
            return Err(MarketIdError::InvalidFormat);
        }
        Ok(Self(value.to_owned()))
    }

    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn market_id_is_a_slug() {
        for valid in [
            "berlin",
            "be",
            "new-york-2",
            &format!("a{}", "b".repeat(31)),
        ] {
            assert_eq!(
                MarketId::new(valid).map(|market| market.as_str().to_owned()),
                Ok(valid.to_owned())
            );
        }
        assert_eq!(MarketId::new(""), Err(MarketIdError::Empty));
        for invalid in [
            "b",
            "Berlin",
            " berlin",
            "berlin ",
            "1berlin",
            "-berlin",
            "ber_lin",
            "berlín",
            &format!("a{}", "b".repeat(32)),
        ] {
            assert_eq!(
                MarketId::new(invalid),
                Err(MarketIdError::InvalidFormat),
                "{invalid:?}"
            );
        }
    }
}
