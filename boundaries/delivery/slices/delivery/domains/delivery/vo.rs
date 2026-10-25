use super::DeliveryValidationError;

fn normalize(value: &str) -> String {
    value.split_whitespace().collect::<Vec<_>>().join(" ")
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Address(String);

impl Address {
    pub fn new(value: &str) -> Result<Self, DeliveryValidationError> {
        let normalized = normalize(value);
        if normalized.is_empty() {
            return Err(DeliveryValidationError::EmptyAddress);
        }
        Ok(Self(normalized))
    }

    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

/// Reason reported for a Delivery Failure. Retryability of the reason is not
/// decided by `delivery`.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct DeliveryFailureReason(String);

impl DeliveryFailureReason {
    pub fn new(value: &str) -> Result<Self, DeliveryValidationError> {
        let normalized = normalize(value);
        if normalized.is_empty() {
            return Err(DeliveryValidationError::EmptyFailureReason);
        }
        Ok(Self(normalized))
    }

    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

/// Free-form reason of a Delivery Cancellation supplied by the requester.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct DeliveryCancellationReason(String);

impl DeliveryCancellationReason {
    /// Normalizes the reason; a blank value means no reason was given.
    #[must_use]
    pub fn new(value: &str) -> Option<Self> {
        let normalized = normalize(value);
        (!normalized.is_empty()).then_some(Self(normalized))
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
    fn address_and_failure_reason_are_normalized() {
        assert_eq!(
            Address::new("  Main   street\t1 ")
                .expect("valid address")
                .as_str(),
            "Main street 1"
        );
        assert_eq!(
            Address::new(" \n ").expect_err("empty address"),
            DeliveryValidationError::EmptyAddress
        );
        assert_eq!(
            DeliveryFailureReason::new(" door   closed ")
                .expect("reason")
                .as_str(),
            "door closed"
        );
        assert_eq!(
            DeliveryFailureReason::new("  ").expect_err("empty reason"),
            DeliveryValidationError::EmptyFailureReason
        );
        assert_eq!(
            DeliveryCancellationReason::new(" order   cancelled ")
                .expect("reason")
                .as_str(),
            "order cancelled"
        );
        assert_eq!(DeliveryCancellationReason::new(" \t "), None);
    }
}
