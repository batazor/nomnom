//! Fixtures shared by the `delivery` slice handler tests.

use crate::infrastructure::memory::{
    FixedRandomSource, ManualClock, RecordingAssignment, RecordingRelationships, RelationshipWrite,
    SequentialIdGenerator, StaticAuthorization, StaticCourierLocator,
};
use crate::infrastructure::repository::test_support::TestStore;
use crate::pkg::authz::{RelationshipWriteError, Subject};
use crate::pkg::persistence::{AuthorizationRevision, RepositoryError};
use crate::slices::courier::applications::update_location::{
    CourierLocationStore, CourierPosition,
};
use crate::slices::courier::domains::courier::{
    Courier, CourierId, CourierName, CourierRepository, CourierStatus, Location, MarketId,
};
use crate::slices::delivery::domains::delivery::{
    Delivery, DeliveryId, DeliveryOffer, DeliveryOfferId, DeliveryOfferRepository,
    DeliveryRepository, DeliveryTracking, Intersection, OfferState, OrderId, State,
    TrackingChannel, VirtualCity,
};

use super::courier_locator::CourierCandidate;
use super::offers_of_delivery::load_offers_of;
use super::policy::AssignmentPolicy;
use super::relationships::DeliveryRelationshipWriter;

pub const NOW: i64 = 1_000;
pub const OFFER_TTL: i64 = 60;

pub struct World {
    pub store: TestStore,
    pub authorization: StaticAuthorization,
    pub clock: ManualClock,
    pub ids: SequentialIdGenerator,
    pub locator: StaticCourierLocator,
    pub policy: AssignmentPolicy,
    pub relationships: RecordingRelationships,
    pub city: VirtualCity,
    pub random: FixedRandomSource,
    pub assignment: RecordingAssignment,
}

impl World {
    pub fn new() -> Self {
        Self::with_authorization(StaticAuthorization::allow_all())
    }

    pub fn with_authorization(authorization: StaticAuthorization) -> Self {
        Self {
            store: TestStore::new(),
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
            city: city(),
            random: FixedRandomSource::new(tracking_bytes()),
            assignment: RecordingAssignment::new(),
        }
    }

    /// Courier of the launch Market `berlin`, the Market of every seeded
    /// Delivery.
    pub fn courier(&self, id: &str, status: CourierStatus) {
        self.courier_in(id, status, &berlin());
    }

    pub fn courier_in(&self, id: &str, status: CourierStatus, market_id: &MarketId) {
        CourierRepository::add(
            &self.store,
            &Courier::restore(
                CourierId::new(id),
                CourierName::new("Timur").expect("courier name"),
                market_id.clone(),
                status,
            ),
        )
        .expect("seed courier");
    }

    pub fn waiting_delivery(&self, id: &str) -> Delivery {
        self.waiting_delivery_in(id, &berlin())
    }

    pub fn waiting_delivery_in(&self, id: &str, market_id: &MarketId) -> Delivery {
        let delivery = Delivery::new(
            DeliveryId::new(id),
            OrderId::new(format!("order-of-{id}")),
            market_id.clone(),
        );
        DeliveryRepository::add(&self.store, &delivery).expect("seed delivery");
        delivery
    }

    /// Delivery assigned to a busy courier.
    pub fn assigned_delivery(&self, id: &str, courier: &str) {
        self.courier(courier, CourierStatus::Busy);
        let mut delivery = new_delivery(id);
        delivery
            .assign(CourierId::new(courier), None)
            .expect("assign");
        DeliveryRepository::add(&self.store, &delivery).expect("seed delivery");
    }

    /// Delivery picked up by a busy courier.
    pub fn picked_up_delivery(&self, id: &str, courier: &str) {
        self.courier(courier, CourierStatus::Busy);
        let courier_id = CourierId::new(courier);
        let mut delivery = new_delivery(id);
        delivery.assign(courier_id.clone(), None).expect("assign");
        delivery.confirm_pickup(&courier_id, None).expect("pickup");
        DeliveryRepository::add(&self.store, &delivery).expect("seed delivery");
    }

    /// Delivery with [`tracking`] assigned to (or, with `picked_up`, picked
    /// up by) a busy courier.
    pub fn tracked_delivery(&self, id: &str, courier: &str, picked_up: bool) {
        self.courier(courier, CourierStatus::Busy);
        let courier_id = CourierId::new(courier);
        let mut delivery = new_delivery(id).with_tracking(Some(tracking()));
        delivery.assign(courier_id.clone(), None).expect("assign");
        if picked_up {
            delivery.confirm_pickup(&courier_id, None).expect("pickup");
        }
        DeliveryRepository::add(&self.store, &delivery).expect("seed delivery");
    }

    /// Stored position of a courier recorded at `recorded_at`.
    pub fn courier_position(&self, courier: &str, latitude: f64, longitude: f64, recorded_at: i64) {
        CourierLocationStore::record(
            &self.store,
            &CourierPosition {
                courier_id: CourierId::new(courier),
                location: Location::new(latitude, longitude).expect("location"),
                recorded_at_epoch_seconds: recorded_at,
            },
            0,
        )
        .expect("seed position");
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

    /// Offers of the delivery, in creation order.
    pub fn offers_of(&self, delivery_id: &str) -> Result<Vec<DeliveryOffer>, RepositoryError> {
        load_offers_of(&self.store, &self.store, &DeliveryId::new(delivery_id))
    }

    pub fn offer_state(&self, id: &str) -> OfferState {
        self.store_offer(id).state()
    }
}

pub fn berlin() -> MarketId {
    MarketId::new("berlin").expect("market id")
}

/// Virtual City of the tests: 12 × 12 blocks of 150 m.
pub fn city() -> VirtualCity {
    VirtualCity::new(52.515, 13.385, 12, 150.0).expect("virtual city")
}

/// Random bytes of [`World::random`]: pickup index 16, dropoff offset 9, the
/// channel `cd…cd`.
fn tracking_bytes() -> Vec<u8> {
    let mut bytes = vec![0, 0, 0, 16, 0, 0, 0, 9];
    bytes.extend([0xcd; 16]);
    bytes
}

/// Tracking that [`World::random`] produces first in [`city`]: pickup
/// (3, 1), dropoff index 26 — (0, 2).
pub fn tracking() -> DeliveryTracking {
    DeliveryTracking {
        pickup: Intersection { x: 3, y: 1 },
        dropoff: Intersection { x: 0, y: 2 },
        channel: TrackingChannel::restore("cd".repeat(16)).expect("channel"),
    }
}

pub fn hamburg() -> MarketId {
    MarketId::new("hamburg").expect("market id")
}

/// Waiting Delivery of the Market `berlin`.
pub fn new_delivery(id: &str) -> Delivery {
    Delivery::new(
        DeliveryId::new(id),
        OrderId::new(format!("order-of-{id}")),
        berlin(),
    )
}

pub fn subject(id: &str) -> Subject {
    Subject::User(id.to_owned())
}

pub fn service() -> Subject {
    Subject::Service("assignment-process".to_owned())
}

/// Candidate of the Market `berlin`.
pub fn candidate(courier: &str, distance_to_pickup_meters: u32) -> CourierCandidate {
    CourierCandidate {
        courier_id: CourierId::new(courier),
        market_id: berlin(),
        within_delivery_zone: true,
        distance_to_pickup_meters,
        active_assignments: 0,
    }
}

/// Relationship writer that records, for every write, the `courier_id` of the
/// delivery as persisted at the moment of the write. It proves that writers
/// run before the use case saves the delivery.
pub struct ObservingRelationships<'a> {
    pub store: &'a TestStore,
    pub observed: std::cell::RefCell<Vec<(RelationshipWrite, Option<CourierId>)>>,
}

impl<'a> ObservingRelationships<'a> {
    pub fn new(store: &'a TestStore) -> Self {
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
