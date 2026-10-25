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

/// Moment the courier expects to reach a step of the Delivery (FR-610), in
/// epoch seconds. Named by the courier; a newly named one is after the
/// current time.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ExpectedTime(i64);

impl ExpectedTime {
    /// A moment named now: it must be after `now_epoch_seconds`; `field`
    /// names the request field in the error.
    pub fn named(
        field: &'static str,
        epoch_seconds: i64,
        now_epoch_seconds: i64,
    ) -> Result<Self, DeliveryValidationError> {
        if epoch_seconds <= now_epoch_seconds {
            return Err(DeliveryValidationError::ExpectedTimeNotInFuture { field });
        }
        Ok(Self(epoch_seconds))
    }

    /// A stored moment: it was in the future when it was named.
    #[must_use]
    pub const fn restore(epoch_seconds: i64) -> Self {
        Self(epoch_seconds)
    }

    #[must_use]
    pub const fn epoch_seconds(self) -> i64 {
        self.0
    }
}

/// Code of the Delivery Failure Reason catalog: a closed set. Retryability
/// of a code is not decided by `delivery`.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub enum DeliveryFailureCode {
    CustomerUnreachable,
    AddressNotFound,
    NoAccess,
    CustomerRefused,
    OrderDamaged,
    CourierIncident,
    /// None of the above; the detail says what happened.
    Other,
}

impl DeliveryFailureCode {
    pub const ALL: [Self; 7] = [
        Self::CustomerUnreachable,
        Self::AddressNotFound,
        Self::NoAccess,
        Self::CustomerRefused,
        Self::OrderDamaged,
        Self::CourierIncident,
        Self::Other,
    ];

    /// Wire values of [`Self::ALL`], in the same order: the stored value, the
    /// `reason` of the `DeliveryFailed` Integration Event and the closed set of
    /// the `nomnom.reason` metric attribute.
    pub const CODES: [&'static str; 7] = [
        "customer_unreachable",
        "address_not_found",
        "no_access",
        "customer_refused",
        "order_damaged",
        "courier_incident",
        "other",
    ];

    #[must_use]
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::CustomerUnreachable => "customer_unreachable",
            Self::AddressNotFound => "address_not_found",
            Self::NoAccess => "no_access",
            Self::CustomerRefused => "customer_refused",
            Self::OrderDamaged => "order_damaged",
            Self::CourierIncident => "courier_incident",
            Self::Other => "other",
        }
    }

    /// Parses a code; surrounding whitespace is ignored. A blank value or a
    /// value outside the catalog is a validation error, never `other`.
    pub fn parse(value: &str) -> Result<Self, DeliveryValidationError> {
        let value = value.trim();
        if value.is_empty() {
            return Err(DeliveryValidationError::EmptyFailureReason);
        }
        Self::ALL
            .into_iter()
            .find(|code| code.as_str() == value)
            .ok_or_else(|| DeliveryValidationError::UnknownFailureReason(value.to_owned()))
    }
}

/// Reason reported for a Delivery Failure: a catalog code and an optional
/// free-text detail. The detail is whitespace-normalized and at most
/// [`Self::MAX_DETAIL_CHARS`] characters; it is never a metric attribute.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct DeliveryFailureReason {
    code: DeliveryFailureCode,
    detail: Option<String>,
}

impl DeliveryFailureReason {
    pub const MAX_DETAIL_CHARS: usize = 500;

    /// Validates input: `code` must be in the catalog; a blank `detail` means
    /// no detail; a detail longer than the limit is rejected.
    pub fn new(code: &str, detail: Option<&str>) -> Result<Self, DeliveryValidationError> {
        let code = DeliveryFailureCode::parse(code)?;
        let detail = detail.map(normalize).filter(|detail| !detail.is_empty());
        if detail
            .as_ref()
            .is_some_and(|detail| detail.chars().count() > Self::MAX_DETAIL_CHARS)
        {
            return Err(DeliveryValidationError::FailureDetailTooLong {
                maximum: Self::MAX_DETAIL_CHARS,
            });
        }
        Ok(Self { code, detail })
    }

    /// Reason without a detail.
    #[must_use]
    pub const fn of(code: DeliveryFailureCode) -> Self {
        Self { code, detail: None }
    }

    /// Restores a stored reason and never fails. A stored code outside the
    /// catalog (free text written before the catalog existed) reads back as
    /// `other` with the old text as detail; an over-long detail is cut to the
    /// limit.
    #[must_use]
    pub fn restore(stored_code: &str, stored_detail: Option<&str>) -> Self {
        let detail = stored_detail
            .map(normalize)
            .filter(|detail| !detail.is_empty());
        let (code, detail) = match DeliveryFailureCode::parse(stored_code) {
            Ok(code) => (code, detail),
            Err(_) => {
                let legacy = normalize(stored_code);
                let detail = match (legacy.is_empty(), detail) {
                    (true, detail) => detail,
                    (false, None) => Some(legacy),
                    (false, Some(detail)) => Some(format!("{legacy}: {detail}")),
                };
                (DeliveryFailureCode::Other, detail)
            }
        };
        Self {
            code,
            detail: detail.map(|detail| detail.chars().take(Self::MAX_DETAIL_CHARS).collect()),
        }
    }

    #[must_use]
    pub const fn code(&self) -> DeliveryFailureCode {
        self.code
    }

    /// Wire value of the code.
    #[must_use]
    pub const fn as_str(&self) -> &'static str {
        self.code.as_str()
    }

    #[must_use]
    pub fn detail(&self) -> Option<&str> {
        self.detail.as_deref()
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
    fn address_and_cancellation_reason_are_normalized() {
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
            DeliveryCancellationReason::new(" order   cancelled ")
                .expect("reason")
                .as_str(),
            "order cancelled"
        );
        assert_eq!(DeliveryCancellationReason::new(" \t "), None);
    }
}

#[cfg(test)]
mod failure_reason_tests {
    use super::*;

    #[test]
    fn catalog_codes_round_trip_and_match_wire_values() {
        for (code, wire) in DeliveryFailureCode::ALL
            .into_iter()
            .zip(DeliveryFailureCode::CODES)
        {
            assert_eq!(code.as_str(), wire);
            assert_eq!(DeliveryFailureCode::parse(wire), Ok(code));
        }
        assert_eq!(
            DeliveryFailureCode::parse(" no_access "),
            Ok(DeliveryFailureCode::NoAccess)
        );
    }

    #[test]
    fn unknown_or_blank_code_is_a_validation_error() {
        assert_eq!(
            DeliveryFailureReason::new("door closed", None),
            Err(DeliveryValidationError::UnknownFailureReason(
                "door closed".to_owned()
            ))
        );
        assert_eq!(
            DeliveryFailureReason::new("Customer_Unreachable", None),
            Err(DeliveryValidationError::UnknownFailureReason(
                "Customer_Unreachable".to_owned()
            ))
        );
        assert_eq!(
            DeliveryFailureReason::new("  ", None),
            Err(DeliveryValidationError::EmptyFailureReason)
        );
    }

    #[test]
    fn detail_is_normalized_optional_and_limited() {
        let reason =
            DeliveryFailureReason::new("no_access", Some("  gate   locked\n ")).expect("reason");
        assert_eq!(reason.code(), DeliveryFailureCode::NoAccess);
        assert_eq!(reason.as_str(), "no_access");
        assert_eq!(reason.detail(), Some("gate locked"));
        assert_eq!(
            DeliveryFailureReason::new("other", Some(" \t ")).expect("blank detail"),
            DeliveryFailureReason::of(DeliveryFailureCode::Other)
        );
        let longest = "й".repeat(DeliveryFailureReason::MAX_DETAIL_CHARS);
        assert!(DeliveryFailureReason::new("other", Some(&longest)).is_ok());
        assert_eq!(
            DeliveryFailureReason::new("other", Some(&format!("{longest}x"))),
            Err(DeliveryValidationError::FailureDetailTooLong {
                maximum: DeliveryFailureReason::MAX_DETAIL_CHARS
            })
        );
    }

    #[test]
    fn stored_catalog_reason_restores_as_is() {
        assert_eq!(
            DeliveryFailureReason::restore("order_damaged", Some("box  crushed")),
            DeliveryFailureReason::new("order_damaged", Some("box crushed")).expect("reason")
        );
        assert_eq!(
            DeliveryFailureReason::restore("courier_incident", None),
            DeliveryFailureReason::of(DeliveryFailureCode::CourierIncident)
        );
    }

    #[test]
    fn legacy_free_text_reads_back_as_other_with_the_text_as_detail() {
        let legacy = DeliveryFailureReason::restore("customer  unreachable", None);
        assert_eq!(legacy.code(), DeliveryFailureCode::Other);
        assert_eq!(legacy.detail(), Some("customer unreachable"));
        assert_eq!(
            DeliveryFailureReason::restore("door closed", Some("rang twice")).detail(),
            Some("door closed: rang twice")
        );
        assert_eq!(
            DeliveryFailureReason::restore("", None),
            DeliveryFailureReason::of(DeliveryFailureCode::Other)
        );
        let long = DeliveryFailureReason::restore(&"x".repeat(900), None);
        assert_eq!(
            long.detail().map(|detail| detail.chars().count()),
            Some(DeliveryFailureReason::MAX_DETAIL_CHARS)
        );
    }
}
