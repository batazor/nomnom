mod acceptance;
mod transitions;

use std::error::Error;
use std::fmt::{Display, Formatter};

use super::DeliveryId;
use crate::slices::courier::domains::courier::CourierId;

pub use acceptance::*;
use transitions::offer_transition;
#[cfg(test)]
use transitions::{ALL_OFFER_STATES, ALL_OFFER_TRIGGERS, OFFER_TRANSITION_TABLE};
pub use transitions::{OfferState, OfferTransitionResult, OfferTrigger};

#[derive(Clone, Debug, Eq, Hash, PartialEq)]
pub struct DeliveryOfferId(String);

impl DeliveryOfferId {
    #[must_use]
    pub fn new(value: impl Into<String>) -> Self {
        Self(value.into())
    }

    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum DeliveryOfferError {
    InvalidTransition {
        from: OfferState,
        trigger: OfferTrigger,
    },
    AlreadyInState {
        state: OfferState,
    },
    /// The acting courier is not the addressee of the offer.
    NotAddressedToCourier,
    /// The offer is past its `expires_at`.
    Expired,
    /// `DeliveryOfferAcceptable` is not satisfied.
    Unacceptable {
        reasons: Vec<DeliveryOfferReason>,
    },
    /// `expires_at` is not later than `offered_at`.
    InvalidExpiry,
}

impl Display for DeliveryOfferError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidTransition { from, trigger } => write!(
                formatter,
                "invalid delivery offer transition from {from:?} with {trigger:?}"
            ),
            Self::AlreadyInState { state } => {
                write!(formatter, "delivery offer is already in state {state:?}")
            }
            Self::NotAddressedToCourier => {
                formatter.write_str("delivery offer is addressed to another courier")
            }
            Self::Expired => formatter.write_str("delivery offer has expired"),
            Self::Unacceptable { reasons } => {
                write!(formatter, "delivery offer is not acceptable: {reasons:?}")
            }
            Self::InvalidExpiry => {
                formatter.write_str("delivery offer must expire after it is offered")
            }
        }
    }
}

impl Error for DeliveryOfferError {}

/// Values outside the offer that `DeliveryOfferAcceptable` needs at the moment
/// of acceptance. The application supplies `now` and the courier snapshot.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct OfferAcceptanceConditions {
    pub now_epoch_seconds: i64,
    pub distance_to_pickup_meters: u32,
    pub maximum_distance_meters: u32,
    pub active_assignments: u32,
    pub maximum_active_assignments: u32,
}

/// Time-limited proposal to one courier to accept one delivery. It does not
/// assign the courier by itself.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct DeliveryOffer {
    id: DeliveryOfferId,
    delivery_id: DeliveryId,
    courier_id: CourierId,
    offered_at_epoch_seconds: i64,
    expires_at_epoch_seconds: i64,
    state: OfferState,
    /// Technical field: optimistic concurrency version of the stored snapshot.
    version: u64,
}

impl DeliveryOffer {
    pub fn new(
        id: DeliveryOfferId,
        delivery_id: DeliveryId,
        courier_id: CourierId,
        offered_at_epoch_seconds: i64,
        expires_at_epoch_seconds: i64,
    ) -> Result<Self, DeliveryOfferError> {
        Self::restore(
            id,
            delivery_id,
            courier_id,
            offered_at_epoch_seconds,
            expires_at_epoch_seconds,
            OfferState::Pending,
        )
    }

    pub fn restore(
        id: DeliveryOfferId,
        delivery_id: DeliveryId,
        courier_id: CourierId,
        offered_at_epoch_seconds: i64,
        expires_at_epoch_seconds: i64,
        state: OfferState,
    ) -> Result<Self, DeliveryOfferError> {
        if expires_at_epoch_seconds <= offered_at_epoch_seconds {
            return Err(DeliveryOfferError::InvalidExpiry);
        }
        Ok(Self {
            id,
            delivery_id,
            courier_id,
            offered_at_epoch_seconds,
            expires_at_epoch_seconds,
            state,
            version: 0,
        })
    }

    /// Snapshot field `version` (optimistic concurrency): the stored version
    /// this offer was loaded at; `0` for a new offer.
    #[must_use]
    pub fn with_version(mut self, version: u64) -> Self {
        self.version = version;
        self
    }

    #[must_use]
    pub const fn version(&self) -> u64 {
        self.version
    }

    /// Pending and not past its deadline; the deadline itself is inclusive.
    #[must_use]
    pub fn is_open_at(&self, now_epoch_seconds: i64) -> bool {
        self.state == OfferState::Pending && now_epoch_seconds <= self.expires_at_epoch_seconds
    }

    pub fn accept(
        &mut self,
        courier_id: &CourierId,
        conditions: OfferAcceptanceConditions,
    ) -> Result<OfferTransitionResult, DeliveryOfferError> {
        self.ensure_addressed_to(courier_id)?;
        let policy = delivery_offer_acceptable(DeliveryOfferContext {
            now_epoch_seconds: conditions.now_epoch_seconds,
            expires_at_epoch_seconds: self.expires_at_epoch_seconds,
            distance_to_pickup_meters: conditions.distance_to_pickup_meters,
            maximum_distance_meters: conditions.maximum_distance_meters,
            active_assignments: conditions.active_assignments,
            maximum_active_assignments: conditions.maximum_active_assignments,
        });
        if !policy.satisfied() {
            return Err(DeliveryOfferError::Unacceptable {
                reasons: policy.reasons,
            });
        }
        self.apply(OfferTrigger::Accept)
    }

    pub fn reject(
        &mut self,
        courier_id: &CourierId,
        now_epoch_seconds: i64,
    ) -> Result<OfferTransitionResult, DeliveryOfferError> {
        self.ensure_addressed_to(courier_id)?;
        let result = offer_transition(self.state, OfferTrigger::Reject)?;
        if now_epoch_seconds > self.expires_at_epoch_seconds {
            return Err(DeliveryOfferError::Expired);
        }
        self.state = result.to;
        Ok(result)
    }

    /// Closes a pending offer after its delivery was assigned through another
    /// offer.
    pub fn withdraw(&mut self) -> Result<OfferTransitionResult, DeliveryOfferError> {
        self.apply(OfferTrigger::Withdraw)
    }

    fn ensure_addressed_to(&self, courier_id: &CourierId) -> Result<(), DeliveryOfferError> {
        if &self.courier_id != courier_id {
            return Err(DeliveryOfferError::NotAddressedToCourier);
        }
        Ok(())
    }

    fn apply(
        &mut self,
        trigger: OfferTrigger,
    ) -> Result<OfferTransitionResult, DeliveryOfferError> {
        let result = offer_transition(self.state, trigger)?;
        self.state = result.to;
        Ok(result)
    }

    #[must_use]
    pub fn id(&self) -> &DeliveryOfferId {
        &self.id
    }

    #[must_use]
    pub fn delivery_id(&self) -> &DeliveryId {
        &self.delivery_id
    }

    #[must_use]
    pub fn courier_id(&self) -> &CourierId {
        &self.courier_id
    }

    #[must_use]
    pub const fn offered_at_epoch_seconds(&self) -> i64 {
        self.offered_at_epoch_seconds
    }

    #[must_use]
    pub const fn expires_at_epoch_seconds(&self) -> i64 {
        self.expires_at_epoch_seconds
    }

    #[must_use]
    pub const fn state(&self) -> OfferState {
        self.state
    }
}

#[cfg(test)]
mod tests;
