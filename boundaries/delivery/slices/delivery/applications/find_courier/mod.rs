//! Find Courier: the assignment process selects the nearest available and
//! eligible courier of the Delivery's Market (ADR 41) for a waiting Delivery,
//! or reports that there is none.
//! No offer or assignment is created and no state changes.
//!
//! [`select_candidate`] is the application service without authorization;
//! Replan Delivery and Dispatch Deliveries reuse it in their Unit of Work.

use std::collections::HashSet;

use crate::pkg::application::Subject;
use crate::slices::courier::applications::assignment::{CourierAssignment, CourierAssignmentError};
use crate::slices::courier::domains::courier::{
    CourierEligibilityContext, CourierEligibilityReason, CourierError, CourierId,
    courier_eligible_for_delivery,
};
use crate::slices::delivery::applications::offers_of_delivery::{OffersOfDelivery, load_offers_of};
use crate::slices::delivery::domains::delivery::{
    Delivery, DeliveryId, DeliveryOfferRepository, DeliveryRepository,
};

use super::authorization::{DeliveryAuthorization, DeliveryPermission, DeliveryResource};
use super::courier_locator::{CourierCandidate, CourierLocator};
use super::error::DeliveryApplicationError;
use super::policy::AssignmentPolicy;
use super::{delivery_id, load_delivery};

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Input {
    pub delivery_id: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum CandidateRejectionReason {
    CourierNotRegistered,
    NotAvailable(CourierError),
    /// The courier already received an offer of this delivery.
    AlreadyOffered,
    Ineligible(Vec<CourierEligibilityReason>),
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct CandidateRejection {
    pub courier_id: CourierId,
    pub reason: CandidateRejectionReason,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum Outcome {
    CandidateFound {
        courier_id: CourierId,
        distance_to_pickup_meters: u32,
    },
    NoCandidate,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Output {
    pub delivery_id: DeliveryId,
    pub outcome: Outcome,
    /// Every considered courier that was not selectable, with its reason.
    pub rejections: Vec<CandidateRejection>,
}

/// Result of [`select_candidate`].
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Selection {
    pub outcome: Outcome,
    /// Every considered courier that was not selectable, with its reason.
    pub rejections: Vec<CandidateRejection>,
}

pub struct Handler<'a> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub deliveries: &'a dyn DeliveryRepository,
    pub couriers: &'a dyn CourierAssignment,
    pub offers: &'a dyn DeliveryOfferRepository,
    pub offers_of: &'a dyn OffersOfDelivery,
    pub locator: &'a dyn CourierLocator,
    pub policy: AssignmentPolicy,
}

impl Handler<'_> {
    pub fn execute(
        &self,
        subject: &Subject,
        input: Input,
    ) -> Result<Output, DeliveryApplicationError> {
        let delivery_id = delivery_id(&input.delivery_id)?;
        self.authorization.check(
            subject,
            DeliveryPermission::FindCourier,
            &DeliveryResource::Delivery(delivery_id.clone()),
        )?;

        let delivery = load_delivery(self.deliveries, &delivery_id)?;
        let Selection {
            outcome,
            rejections,
        } = select_candidate(
            &delivery,
            self.offers_of,
            self.offers,
            self.couriers,
            self.locator,
            self.policy,
        )?;
        Ok(Output {
            delivery_id,
            outcome,
            rejections,
        })
    }
}

/// Selects the nearest available and eligible courier for an assignable
/// delivery; ties are broken by courier id. Changes no state.
pub fn select_candidate(
    delivery: &Delivery,
    offers_of: &dyn OffersOfDelivery,
    offers: &dyn DeliveryOfferRepository,
    couriers: &dyn CourierAssignment,
    locator: &dyn CourierLocator,
    policy: AssignmentPolicy,
) -> Result<Selection, DeliveryApplicationError> {
    delivery.ensure_assignable()?;
    let offered: HashSet<CourierId> = load_offers_of(offers_of, offers, delivery.id())?
        .into_iter()
        .map(|offer| offer.courier_id().clone())
        .collect();

    let mut eligible = Vec::new();
    let mut rejections = Vec::new();
    for candidate in locator.candidates(delivery.market_id())? {
        match evaluate(delivery, &candidate, &offered, couriers, policy)? {
            Some(reason) => rejections.push(CandidateRejection {
                courier_id: candidate.courier_id,
                reason,
            }),
            None => eligible.push(candidate),
        }
    }

    let outcome = eligible
        .into_iter()
        .min_by(|left, right| {
            left.distance_to_pickup_meters
                .cmp(&right.distance_to_pickup_meters)
                .then_with(|| left.courier_id.cmp(&right.courier_id))
        })
        .map_or(Outcome::NoCandidate, |selected| Outcome::CandidateFound {
            courier_id: selected.courier_id,
            distance_to_pickup_meters: selected.distance_to_pickup_meters,
        });
    Ok(Selection {
        outcome,
        rejections,
    })
}

/// Returns why the candidate cannot be selected, or `None` if it can. A
/// courier of another Market is never selectable (ADR 41): the
/// `CourierEligibleForDelivery` specification checks the candidate's Market
/// and the `courier` slice port the registered courier's.
fn evaluate(
    delivery: &Delivery,
    candidate: &CourierCandidate,
    offered: &HashSet<CourierId>,
    couriers: &dyn CourierAssignment,
    policy: AssignmentPolicy,
) -> Result<Option<CandidateRejectionReason>, DeliveryApplicationError> {
    if offered.contains(&candidate.courier_id) {
        return Ok(Some(CandidateRejectionReason::AlreadyOffered));
    }
    let eligibility = courier_eligible_for_delivery(CourierEligibilityContext {
        delivery_market_id: delivery.market_id(),
        courier_market_id: &candidate.market_id,
        within_delivery_zone: candidate.within_delivery_zone,
        distance_to_pickup_meters: candidate.distance_to_pickup_meters,
        maximum_distance_meters: policy.maximum_distance_meters,
        active_assignments: candidate.active_assignments,
        maximum_active_assignments: policy.maximum_active_assignments,
    });
    if eligibility
        .reasons
        .contains(&CourierEligibilityReason::OtherMarket)
    {
        return Ok(Some(CandidateRejectionReason::Ineligible(
            eligibility.reasons,
        )));
    }
    match couriers.ensure_available(&candidate.courier_id, delivery.market_id()) {
        Ok(()) => {}
        Err(CourierAssignmentError::CourierNotFound(_)) => {
            return Ok(Some(CandidateRejectionReason::CourierNotRegistered));
        }
        Err(CourierAssignmentError::Precondition(error)) => {
            return Ok(Some(CandidateRejectionReason::NotAvailable(error)));
        }
        Err(error @ CourierAssignmentError::Repository(_)) => return Err(error.into()),
    }
    if eligibility.satisfied() {
        Ok(None)
    } else {
        Ok(Some(CandidateRejectionReason::Ineligible(
            eligibility.reasons,
        )))
    }
}

#[cfg(test)]
mod tests;
