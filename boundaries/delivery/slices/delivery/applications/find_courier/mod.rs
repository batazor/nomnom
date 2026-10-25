//! Find Courier: the assignment process selects the nearest available and
//! eligible courier for a waiting Delivery, or reports that there is none.
//! No offer or assignment is created and no state changes.

use std::collections::HashSet;

use crate::pkg::application::Subject;
use crate::slices::courier::applications::assignment::{CourierAssignment, CourierAssignmentError};
use crate::slices::courier::domains::courier::{
    CourierEligibilityContext, CourierEligibilityReason, CourierError, CourierId,
    courier_eligible_for_delivery,
};
use crate::slices::delivery::domains::delivery::{
    DeliveryId, DeliveryOfferRepository, DeliveryRepository,
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

pub struct Handler<'a> {
    pub authorization: &'a dyn DeliveryAuthorization,
    pub deliveries: &'a dyn DeliveryRepository,
    pub couriers: &'a dyn CourierAssignment,
    pub offers: &'a dyn DeliveryOfferRepository,
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
        delivery.ensure_assignable()?;
        let offered: HashSet<CourierId> = self
            .offers
            .list_for_delivery(&delivery_id)?
            .into_iter()
            .map(|offer| offer.courier_id().clone())
            .collect();

        let mut eligible = Vec::new();
        let mut rejections = Vec::new();
        for candidate in self.locator.candidates_near(delivery.pickup_address())? {
            match self.evaluate(&candidate, &offered)? {
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
        Ok(Output {
            delivery_id,
            outcome,
            rejections,
        })
    }

    /// Returns why the candidate cannot be selected, or `None` if it can.
    fn evaluate(
        &self,
        candidate: &CourierCandidate,
        offered: &HashSet<CourierId>,
    ) -> Result<Option<CandidateRejectionReason>, DeliveryApplicationError> {
        if offered.contains(&candidate.courier_id) {
            return Ok(Some(CandidateRejectionReason::AlreadyOffered));
        }
        match self.couriers.ensure_available(&candidate.courier_id) {
            Ok(()) => {}
            Err(CourierAssignmentError::CourierNotFound(_)) => {
                return Ok(Some(CandidateRejectionReason::CourierNotRegistered));
            }
            Err(CourierAssignmentError::Precondition(error)) => {
                return Ok(Some(CandidateRejectionReason::NotAvailable(error)));
            }
            Err(error @ CourierAssignmentError::Repository(_)) => return Err(error.into()),
        }
        let eligibility = courier_eligible_for_delivery(CourierEligibilityContext {
            within_delivery_zone: candidate.within_delivery_zone,
            distance_to_pickup_meters: candidate.distance_to_pickup_meters,
            maximum_distance_meters: self.policy.maximum_distance_meters,
            active_assignments: candidate.active_assignments,
            maximum_active_assignments: self.policy.maximum_active_assignments,
        });
        if eligibility.satisfied() {
            Ok(None)
        } else {
            Ok(Some(CandidateRejectionReason::Ineligible(
                eligibility.reasons,
            )))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::infrastructure::memory::StaticAuthorization;
    use crate::slices::courier::domains::courier::CourierStatus;
    use crate::slices::delivery::applications::test_support::{World, candidate, service};
    use crate::slices::delivery::domains::delivery::{DeliveryError, State, TransitionError};

    fn handler(world: &World) -> Handler<'_> {
        Handler {
            authorization: &world.authorization,
            deliveries: &world.store,
            couriers: &world.store,
            offers: &world.store,
            locator: &world.locator,
            policy: world.policy,
        }
    }

    fn input() -> Input {
        Input {
            delivery_id: "delivery-1".to_owned(),
        }
    }

    /// AC-317
    #[test]
    fn selects_nearest_available_eligible_courier() {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        world.courier("far", CourierStatus::Available);
        world.courier("near", CourierStatus::Available);
        world.courier("tie", CourierStatus::Available);
        world.locator.set_candidates(vec![
            candidate("far", 4_000),
            candidate("tie", 1_000),
            candidate("near", 1_000),
        ]);

        let output = handler(&world)
            .execute(&service(), input())
            .expect("search");

        assert_eq!(
            output.outcome,
            Outcome::CandidateFound {
                courier_id: CourierId::new("near"),
                distance_to_pickup_meters: 1_000,
            }
        );
        assert!(output.rejections.is_empty());
        assert_eq!(world.delivery_state("delivery-1"), State::Waiting);
        assert_eq!(world.courier_status("near"), CourierStatus::Available);
        assert_eq!(world.store.commits(), 0);
    }

    /// AC-318
    #[test]
    fn reports_no_candidate_with_reason_per_courier() {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        world.courier("busy", CourierStatus::Busy);
        world.courier("off", CourierStatus::Unavailable);
        world.courier("outside", CourierStatus::Available);
        world.courier("loaded", CourierStatus::Available);
        world.courier("offered", CourierStatus::Available);
        world.offer("offer-1", "delivery-1", "offered");
        let mut outside = candidate("outside", 6_000);
        outside.within_delivery_zone = false;
        let mut loaded = candidate("loaded", 100);
        loaded.active_assignments = 1;
        world.locator.set_candidates(vec![
            candidate("busy", 100),
            candidate("off", 100),
            outside,
            loaded,
            candidate("offered", 100),
            candidate("ghost", 100),
        ]);

        let output = handler(&world)
            .execute(&service(), input())
            .expect("search");

        assert_eq!(output.outcome, Outcome::NoCandidate);
        let rejection = |courier: &str, reason| CandidateRejection {
            courier_id: CourierId::new(courier),
            reason,
        };
        assert_eq!(
            output.rejections,
            vec![
                rejection(
                    "busy",
                    CandidateRejectionReason::NotAvailable(CourierError::AlreadyBusy)
                ),
                rejection(
                    "off",
                    CandidateRejectionReason::NotAvailable(CourierError::Unavailable)
                ),
                rejection(
                    "outside",
                    CandidateRejectionReason::Ineligible(vec![
                        CourierEligibilityReason::OutsideDeliveryZone,
                        CourierEligibilityReason::TooFarFromPickup,
                    ])
                ),
                rejection(
                    "loaded",
                    CandidateRejectionReason::Ineligible(vec![
                        CourierEligibilityReason::ActiveAssignmentLimitReached
                    ])
                ),
                rejection("offered", CandidateRejectionReason::AlreadyOffered),
                rejection("ghost", CandidateRejectionReason::CourierNotRegistered),
            ]
        );
    }

    /// AC-319
    #[test]
    fn assigned_delivery_is_not_searched() {
        let world = World::new();
        world.assigned_delivery("delivery-1", "courier-1");

        assert_eq!(
            handler(&world).execute(&service(), input()),
            Err(DeliveryApplicationError::DeliveryPrecondition(
                DeliveryError::Transition(TransitionError::AlreadyInState {
                    state: State::Assigned
                })
            ))
        );
    }

    #[test]
    fn unknown_delivery_is_not_found() {
        let world = World::new();
        assert_eq!(
            handler(&world).execute(&service(), input()),
            Err(DeliveryApplicationError::DeliveryNotFound(DeliveryId::new(
                "delivery-1"
            )))
        );
    }

    #[test]
    fn locator_outage_is_dependency_unavailable() {
        let world = World::new();
        world.waiting_delivery("delivery-1");
        world.locator.set_unavailable(true);
        assert!(matches!(
            handler(&world).execute(&service(), input()),
            Err(DeliveryApplicationError::DependencyUnavailable(_))
        ));
    }

    #[test]
    fn denied_subject_cannot_search() {
        let world = World::with_authorization(StaticAuthorization::deny_all());
        world.waiting_delivery("delivery-1");
        assert_eq!(
            handler(&world).execute(&service(), input()),
            Err(DeliveryApplicationError::PermissionDenied)
        );
    }
}
