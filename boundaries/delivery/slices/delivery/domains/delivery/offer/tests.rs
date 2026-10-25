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
