//! Fixtures shared by the `delivery` slice handler tests.

use crate::infrastructure::memory::{
    ManualClock, MemoryStore, RecordingRelationships, RelationshipWrite, SequentialIdGenerator,
    StaticAuthorization, StaticCourierLocator,
};
use crate::pkg::application::{RelationshipWriteError, Subject};
use crate::pkg::persistence::AuthorizationRevision;
use crate::slices::courier::domains::courier::{
    Courier, CourierId, CourierRepository, CourierStatus,
};
use crate::slices::delivery::domains::delivery::{
    Address, Delivery, DeliveryId, DeliveryOffer, DeliveryOfferId, DeliveryOfferRepository,
    DeliveryRepository, OfferState, OrderId, State,
};

use super::courier_locator::CourierCandidate;
use super::policy::AssignmentPolicy;
use super::relationships::DeliveryRelationshipWriter;

pub const NOW: i64 = 1_000;
pub const OFFER_TTL: i64 = 60;

pub struct World {
    pub store: MemoryStore,
    pub authorization: StaticAuthorization,
    pub clock: ManualClock,
    pub ids: SequentialIdGenerator,
    pub locator: StaticCourierLocator,
    pub policy: AssignmentPolicy,
    pub relationships: RecordingRelationships,
}

impl World {
    pub fn new() -> Self {
        Self::with_authorization(StaticAuthorization::allow_all())
    }

    pub fn with_authorization(authorization: StaticAuthorization) -> Self {
        Self {
            store: MemoryStore::new(),
            authorization,
            clock: ManualClock::new(NOW),
            ids: SequentialIdGenerator::new("generated"),
            locator: StaticCourierLocator::default(),
            policy: AssignmentPolicy {
                maximum_distance_meters: 5_000,
                maximum_active_assignments: 1,
                offer_ttl_seconds: OFFER_TTL,
            },
            relationships: RecordingRelationships::new(),
        }
    }

    pub fn courier(&self, id: &str, status: CourierStatus) {
        CourierRepository::add(&self.store, &Courier::restore(CourierId::new(id), status))
            .expect("seed courier");
    }

    pub fn waiting_delivery(&self, id: &str) -> Delivery {
        let delivery = new_delivery(id);
        DeliveryRepository::add(&self.store, &delivery).expect("seed delivery");
        delivery
    }

    /// Delivery assigned to a busy courier.
    pub fn assigned_delivery(&self, id: &str, courier: &str) {
        self.courier(courier, CourierStatus::Busy);
        let mut delivery = new_delivery(id);
        delivery.assign(CourierId::new(courier)).expect("assign");
        DeliveryRepository::add(&self.store, &delivery).expect("seed delivery");
    }

    /// Delivery picked up by a busy courier.
    pub fn picked_up_delivery(&self, id: &str, courier: &str) {
        self.courier(courier, CourierStatus::Busy);
        let courier_id = CourierId::new(courier);
        let mut delivery = new_delivery(id);
        delivery.assign(courier_id.clone()).expect("assign");
        delivery.confirm_pickup(&courier_id).expect("pickup");
        DeliveryRepository::add(&self.store, &delivery).expect("seed delivery");
    }

    pub fn offer(&self, offer_id: &str, delivery_id: &str, courier: &str) {
        let offer = DeliveryOffer::new(
            DeliveryOfferId::new(offer_id),
            DeliveryId::new(delivery_id),
            CourierId::new(courier),
            NOW,
            NOW + OFFER_TTL,
        )
        .expect("offer");
        DeliveryOfferRepository::add(&self.store, &offer).expect("seed offer");
    }

    pub fn delivery(&self, id: &str) -> Delivery {
        DeliveryRepository::get(&self.store, &DeliveryId::new(id))
            .expect("read delivery")
            .expect("delivery exists")
    }

    pub fn delivery_state(&self, id: &str) -> State {
        self.delivery(id).state()
    }

    pub fn courier_status(&self, id: &str) -> CourierStatus {
        CourierRepository::get(&self.store, &CourierId::new(id))
            .expect("read courier")
            .expect("courier exists")
            .status()
    }

    pub fn store_offer(&self, id: &str) -> DeliveryOffer {
        DeliveryOfferRepository::get(&self.store, &DeliveryOfferId::new(id))
            .expect("read offer")
            .expect("offer exists")
    }

    pub fn offer_state(&self, id: &str) -> OfferState {
        self.store_offer(id).state()
    }
}

pub fn new_delivery(id: &str) -> Delivery {
    Delivery::new(
        DeliveryId::new(id),
        OrderId::new(format!("order-of-{id}")),
        Address::new("Restaurant street 1").expect("pickup"),
        Address::new("Customer street 2").expect("dropoff"),
    )
}

pub fn subject(id: &str) -> Subject {
    Subject::User(id.to_owned())
}

pub fn service() -> Subject {
    Subject::Service("assignment-process".to_owned())
}

pub fn candidate(courier: &str, distance_to_pickup_meters: u32) -> CourierCandidate {
    CourierCandidate {
        courier_id: CourierId::new(courier),
        within_delivery_zone: true,
        distance_to_pickup_meters,
        active_assignments: 0,
    }
}

/// Relationship writer that records, for every write, the `courier_id` of the
/// delivery as persisted at the moment of the write. It proves that writers
/// run before the use case saves the delivery.
pub struct ObservingRelationships<'a> {
    pub store: &'a MemoryStore,
    pub observed: std::cell::RefCell<Vec<(RelationshipWrite, Option<CourierId>)>>,
}

impl<'a> ObservingRelationships<'a> {
    pub fn new(store: &'a MemoryStore) -> Self {
        Self {
            store,
            observed: std::cell::RefCell::new(Vec::new()),
        }
    }

    fn observe(&self, write: RelationshipWrite, delivery_id: &DeliveryId) {
        let persisted = DeliveryRepository::get(self.store, delivery_id)
            .expect("read delivery")
            .and_then(|delivery| delivery.courier_id().cloned());
        self.observed.borrow_mut().push((write, persisted));
    }
}

impl DeliveryRelationshipWriter for ObservingRelationships<'_> {
    fn assign_courier(
        &self,
        delivery_id: &DeliveryId,
        courier_id: &CourierId,
    ) -> Result<AuthorizationRevision, RelationshipWriteError> {
        self.observe(
            RelationshipWrite::AssignCourier(delivery_id.clone(), courier_id.clone()),
            delivery_id,
        );
        Ok(AuthorizationRevision::new("observed"))
    }

    fn unassign_courier(
        &self,
        delivery_id: &DeliveryId,
        courier_id: &CourierId,
    ) -> Result<AuthorizationRevision, RelationshipWriteError> {
        self.observe(
            RelationshipWrite::UnassignCourier(delivery_id.clone(), courier_id.clone()),
            delivery_id,
        );
        Ok(AuthorizationRevision::new("observed"))
    }
}
