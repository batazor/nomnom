use std::error::Error;
use std::fmt::{Display, Formatter};

use super::DeliveryId;
use crate::slices::courier::domains::courier::CourierId;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DeliveryOfferReason {
    Expired,
    TooFarFromPickup,
    ActiveAssignmentLimitReached,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct DeliveryOfferResult {
    pub reasons: Vec<DeliveryOfferReason>,
}

impl DeliveryOfferResult {
    #[must_use]
    pub fn satisfied(&self) -> bool {
        self.reasons.is_empty()
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct DeliveryOfferContext {
    pub now_epoch_seconds: i64,
    pub expires_at_epoch_seconds: i64,
    pub distance_to_pickup_meters: u32,
    pub maximum_distance_meters: u32,
    pub active_assignments: u32,
    pub maximum_active_assignments: u32,
}

#[must_use]
pub fn delivery_offer_acceptable(context: DeliveryOfferContext) -> DeliveryOfferResult {
    let mut reasons = Vec::new();
    if context.now_epoch_seconds > context.expires_at_epoch_seconds {
        reasons.push(DeliveryOfferReason::Expired);
    }
    if context.distance_to_pickup_meters > context.maximum_distance_meters {
        reasons.push(DeliveryOfferReason::TooFarFromPickup);
    }
    if context.active_assignments >= context.maximum_active_assignments {
        reasons.push(DeliveryOfferReason::ActiveAssignmentLimitReached);
    }
    DeliveryOfferResult { reasons }
}

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

/// Business state of a Delivery Offer. Expiry is a function of time and
/// `expires_at`, not a stored state.
#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub enum OfferState {
    Pending,
    Accepted,
    Rejected,
    /// Closed because the delivery was assigned through another offer.
    Withdrawn,
}

#[cfg(test)]
const ALL_OFFER_STATES: [OfferState; 4] = [
    OfferState::Pending,
    OfferState::Accepted,
    OfferState::Rejected,
    OfferState::Withdrawn,
];

#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
pub enum OfferTrigger {
    Accept,
    Reject,
    Withdraw,
}

#[cfg(test)]
const ALL_OFFER_TRIGGERS: [OfferTrigger; 3] = [
    OfferTrigger::Accept,
    OfferTrigger::Reject,
    OfferTrigger::Withdraw,
];

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct OfferTransitionResult {
    pub from: OfferState,
    pub trigger: OfferTrigger,
    pub to: OfferState,
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

#[derive(Clone, Copy)]
enum OfferOutcome {
    Allowed(OfferState),
    Invalid,
    AlreadyApplied,
}

#[derive(Clone, Copy)]
struct OfferTransitionDefinition {
    from: OfferState,
    trigger: OfferTrigger,
    outcome: OfferOutcome,
}

const OFFER_TRANSITION_TABLE: [OfferTransitionDefinition; 12] = [
    offer_definition(
        OfferState::Pending,
        OfferTrigger::Accept,
        OfferOutcome::Allowed(OfferState::Accepted),
    ),
    offer_definition(
        OfferState::Pending,
        OfferTrigger::Reject,
        OfferOutcome::Allowed(OfferState::Rejected),
    ),
    offer_definition(
        OfferState::Pending,
        OfferTrigger::Withdraw,
        OfferOutcome::Allowed(OfferState::Withdrawn),
    ),
    offer_definition(
        OfferState::Accepted,
        OfferTrigger::Accept,
        OfferOutcome::AlreadyApplied,
    ),
    offer_definition(
        OfferState::Accepted,
        OfferTrigger::Reject,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Accepted,
        OfferTrigger::Withdraw,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Rejected,
        OfferTrigger::Accept,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Rejected,
        OfferTrigger::Reject,
        OfferOutcome::AlreadyApplied,
    ),
    offer_definition(
        OfferState::Rejected,
        OfferTrigger::Withdraw,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Withdrawn,
        OfferTrigger::Accept,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Withdrawn,
        OfferTrigger::Reject,
        OfferOutcome::Invalid,
    ),
    offer_definition(
        OfferState::Withdrawn,
        OfferTrigger::Withdraw,
        OfferOutcome::AlreadyApplied,
    ),
];

const fn offer_definition(
    from: OfferState,
    trigger: OfferTrigger,
    outcome: OfferOutcome,
) -> OfferTransitionDefinition {
    OfferTransitionDefinition {
        from,
        trigger,
        outcome,
    }
}

fn offer_transition(
    from: OfferState,
    trigger: OfferTrigger,
) -> Result<OfferTransitionResult, DeliveryOfferError> {
    let Some(rule) = OFFER_TRANSITION_TABLE
        .iter()
        .find(|rule| rule.from == from && rule.trigger == trigger)
    else {
        return Err(DeliveryOfferError::InvalidTransition { from, trigger });
    };
    match rule.outcome {
        OfferOutcome::Invalid => Err(DeliveryOfferError::InvalidTransition { from, trigger }),
        OfferOutcome::AlreadyApplied => Err(DeliveryOfferError::AlreadyInState { state: from }),
        OfferOutcome::Allowed(to) => Ok(OfferTransitionResult { from, trigger, to }),
    }
}

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
        })
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
mod tests {
    use super::*;

    fn offer() -> DeliveryOffer {
        DeliveryOffer::new(
            DeliveryOfferId::new("offer-1"),
            DeliveryId::new("delivery-1"),
            CourierId::new("courier-1"),
            1_000,
            1_060,
        )
        .expect("offer")
    }

    fn conditions(now: i64) -> OfferAcceptanceConditions {
        OfferAcceptanceConditions {
            now_epoch_seconds: now,
            distance_to_pickup_meters: 1_000,
            maximum_distance_meters: 5_000,
            active_assignments: 0,
            maximum_active_assignments: 1,
        }
    }

    #[test]
    fn offer_transition_table_is_complete() {
        for state in ALL_OFFER_STATES {
            for trigger in ALL_OFFER_TRIGGERS {
                assert!(
                    OFFER_TRANSITION_TABLE
                        .iter()
                        .any(|rule| rule.from == state && rule.trigger == trigger),
                    "missing outcome for {state:?} x {trigger:?}"
                );
            }
        }
        assert_eq!(
            OFFER_TRANSITION_TABLE.len(),
            ALL_OFFER_STATES.len() * ALL_OFFER_TRIGGERS.len()
        );
    }

    #[test]
    fn closed_offers_reject_other_triggers_and_repeats_are_explicit() {
        for (state, repeated) in [
            (OfferState::Accepted, OfferTrigger::Accept),
            (OfferState::Rejected, OfferTrigger::Reject),
            (OfferState::Withdrawn, OfferTrigger::Withdraw),
        ] {
            for trigger in ALL_OFFER_TRIGGERS {
                let expected = if trigger == repeated {
                    DeliveryOfferError::AlreadyInState { state }
                } else {
                    DeliveryOfferError::InvalidTransition {
                        from: state,
                        trigger,
                    }
                };
                assert_eq!(offer_transition(state, trigger), Err(expected));
            }
        }
    }

    #[test]
    fn addressee_accepts_offer_on_deadline() {
        let mut offer = offer();
        let result = offer
            .accept(&CourierId::new("courier-1"), conditions(1_060))
            .expect("accept on deadline");
        assert_eq!(result.to, OfferState::Accepted);
        assert_eq!(offer.state(), OfferState::Accepted);
        assert!(!offer.is_open_at(1_000));
    }

    #[test]
    fn accept_rejects_other_courier_expired_offer_and_unacceptable_conditions() {
        let mut offer = offer();
        assert_eq!(
            offer.accept(&CourierId::new("courier-2"), conditions(1_000)),
            Err(DeliveryOfferError::NotAddressedToCourier)
        );
        assert_eq!(
            offer.accept(&CourierId::new("courier-1"), conditions(1_061)),
            Err(DeliveryOfferError::Unacceptable {
                reasons: vec![DeliveryOfferReason::Expired]
            })
        );
        let mut far = conditions(1_000);
        far.distance_to_pickup_meters = 5_001;
        assert_eq!(
            offer.accept(&CourierId::new("courier-1"), far),
            Err(DeliveryOfferError::Unacceptable {
                reasons: vec![DeliveryOfferReason::TooFarFromPickup]
            })
        );
        assert_eq!(offer.state(), OfferState::Pending);
    }

    #[test]
    fn addressee_rejects_open_offer_only() {
        let mut offer = offer();
        assert_eq!(
            offer.reject(&CourierId::new("courier-2"), 1_000),
            Err(DeliveryOfferError::NotAddressedToCourier)
        );
        assert_eq!(
            offer.reject(&CourierId::new("courier-1"), 1_061),
            Err(DeliveryOfferError::Expired)
        );
        assert_eq!(
            offer
                .reject(&CourierId::new("courier-1"), 1_060)
                .expect("reject")
                .to,
            OfferState::Rejected
        );
        assert_eq!(
            offer.reject(&CourierId::new("courier-1"), 1_000),
            Err(DeliveryOfferError::AlreadyInState {
                state: OfferState::Rejected
            })
        );
    }

    #[test]
    fn pending_offer_can_be_withdrawn() {
        let mut offer = offer();
        assert_eq!(
            offer.withdraw().expect("withdraw").to,
            OfferState::Withdrawn
        );
        assert!(!offer.is_open_at(1_000));
    }

    #[test]
    fn offer_must_expire_after_it_is_offered() {
        assert_eq!(
            DeliveryOffer::new(
                DeliveryOfferId::new("offer-1"),
                DeliveryId::new("delivery-1"),
                CourierId::new("courier-1"),
                1_000,
                1_000,
            ),
            Err(DeliveryOfferError::InvalidExpiry)
        );
    }
}
