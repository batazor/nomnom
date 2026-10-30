//! Technical ports and input validation shared by the use cases of the
//! `delivery` bounded context.

/// Source of the current time for application code; domain code receives
/// the value explicitly.
pub trait Clock {
    fn now_epoch_seconds(&self) -> i64;
}

/// Generates identifiers of new aggregates before a transaction starts.
pub trait IdGenerator {
    fn next_id(&self) -> String;
}

/// Input validation of a required identifier: surrounding whitespace is
/// removed and an empty value is rejected.
#[must_use]
pub fn required_identifier(value: &str) -> Option<&str> {
    let trimmed = value.trim();
    (!trimmed.is_empty()).then_some(trimmed)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn required_identifier_trims_and_rejects_blank_values() {
        assert_eq!(required_identifier(" courier-1 "), Some("courier-1"));
        assert_eq!(required_identifier(" \t "), None);
    }
}
