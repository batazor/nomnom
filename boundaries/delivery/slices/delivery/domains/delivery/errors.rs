use std::error::Error;
use std::fmt::{Display, Formatter};

use super::TransitionError;

/// Failure of a delivery operation performed on behalf of a courier.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum DeliveryError {
    Transition(TransitionError),
    /// The acting courier is not the courier assigned to the delivery.
    CourierNotAssigned,
}

impl Display for DeliveryError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Transition(error) => Display::fmt(error, formatter),
            Self::CourierNotAssigned => {
                formatter.write_str("courier is not assigned to the delivery")
            }
        }
    }
}

impl Error for DeliveryError {}

impl From<TransitionError> for DeliveryError {
    fn from(error: TransitionError) -> Self {
        Self::Transition(error)
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum DeliveryValidationError {
    EmptyAddress,
    EmptyFailureReason,
    /// The failure reason code is not in the Delivery Failure Reason catalog.
    UnknownFailureReason(String),
    /// The free-text detail of a failure reason exceeds `maximum` characters.
    FailureDetailTooLong {
        maximum: usize,
    },
    AssignedStateRequiresCourier,
    WaitingStateMustNotHaveCourier,
    CancelledStateMustNotHaveCourier,
    FailedStateRequiresReason,
    FailureReasonRequiresFailedState,
    /// An expected time named by the courier is not after the current time.
    ExpectedTimeNotInFuture {
        field: &'static str,
    },
}

impl Display for DeliveryValidationError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::EmptyAddress => formatter.write_str("address must not be empty"),
            Self::EmptyFailureReason => {
                formatter.write_str("delivery failure reason must not be empty")
            }
            Self::UnknownFailureReason(code) => {
                write!(formatter, "unknown delivery failure reason {code:?}")
            }
            Self::FailureDetailTooLong { maximum } => write!(
                formatter,
                "delivery failure detail must be at most {maximum} characters"
            ),
            Self::AssignedStateRequiresCourier => {
                formatter.write_str("assigned delivery requires a courier")
            }
            Self::WaitingStateMustNotHaveCourier => {
                formatter.write_str("waiting delivery must not have a courier")
            }
            Self::CancelledStateMustNotHaveCourier => {
                formatter.write_str("cancelled delivery must not have a courier")
            }
            Self::FailedStateRequiresReason => {
                formatter.write_str("failed delivery requires a failure reason")
            }
            Self::FailureReasonRequiresFailedState => {
                formatter.write_str("only a failed delivery has a failure reason")
            }
            Self::ExpectedTimeNotInFuture { field } => {
                write!(formatter, "{field} must be in the future")
            }
        }
    }
}

impl Error for DeliveryValidationError {}
