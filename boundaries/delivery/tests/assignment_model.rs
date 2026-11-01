//! Трассы модели `verification/quint/assignment.qnt` на настоящих use cases и
//! PostgreSQL из testcontainers: после каждого шага состояние кода сверяется с
//! моделью. Use case выполняет транзакцию одним вызовом, поэтому проигрывается
//! `sequentialStep`, по одному use case за раз; чередования проверяет
//! симуляция модели. Temporal и SpiceDB записываются вместо вызовов: таймеры и
//! пробуждения сверяются как записанные вызовы.
//!
//! Нужен `quint` в PATH: тест запускает `make verify-quint`.

mod support;

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::{SystemTime, UNIX_EPOCH};

use anyhow::{Context, anyhow};
use delivery::infrastructure::memory::{
    AssignmentCall, ManualClock, RecordingAssignment, RecordingRelationships, RelationshipWrite,
    StaticAuthorization, StaticCourierLocator,
};
use delivery::infrastructure::repository::Store;
use delivery::pkg::authz::Subject;
use delivery::slices::courier::applications::update_location::{
    CourierLocationStore, CourierPosition,
};
use delivery::slices::courier::domains::courier::{
    Courier, CourierId, CourierName, CourierRepository, CourierStatus, Location, MarketId,
};
use delivery::slices::delivery::applications::assignment::{advance, reassign_courier};
use delivery::slices::delivery::applications::assignment_process::CandidateDeliveries;
use delivery::slices::delivery::applications::courier_locator::CourierCandidate;
use delivery::slices::delivery::applications::execution::{cancel, deliver, pick_up};
use delivery::slices::delivery::applications::offer::{accept, expire, reject, to_courier};
use delivery::slices::delivery::applications::policy::AssignmentPolicy;
use delivery::slices::delivery::domains::delivery::{
    Delivery, DeliveryId, DeliveryOfferId, DeliveryOfferRepository, DeliveryRepository, OfferState,
    OrderId, State as DeliveryFsmState,
};
use delivery::slices::delivery::infrastructure::DeliveryIntegrationEvents;
use quint_connect::{Config, Driver, Result, State, Step, quint_run, switch};
use serde::Deserialize;
use sqlx::Connection;

const NOW: i64 = 1_000;
const NO_COURIER: i64 = 0;
const POLICY: AssignmentPolicy = AssignmentPolicy {
    maximum_distance_meters: 5_000,
    maximum_active_assignments: 1,
    offer_ttl_seconds: 60,
};

#[derive(Debug, Deserialize, Eq, PartialEq)]
#[serde(tag = "tag")]
enum ModelDeliveryState {
    Waiting,
    Assigned,
    Closed,
}

#[derive(Debug, Deserialize, Eq, PartialEq)]
#[serde(tag = "tag")]
enum ModelCourierStatus {
    Available,
    Busy,
}

#[derive(Debug, Deserialize, Eq, PartialEq)]
#[serde(tag = "tag")]
enum ModelOfferState {
    Pending,
    Accepted,
    Rejected,
    Withdrawn,
    Expired,
}

#[derive(Debug, Deserialize, Eq, PartialEq)]
struct ModelDelivery {
    state: ModelDeliveryState,
    courier: i64,
}

#[derive(Debug, Deserialize, Eq, PartialEq)]
struct ModelCourier {
    status: ModelCourierStatus,
}

#[derive(Debug, Deserialize, Eq, PartialEq)]
struct ModelOffer {
    delivery: i64,
    courier: i64,
    state: ModelOfferState,
}

/// What the model and the code must agree on; the rest of the model is its own
/// bookkeeping (versions, transactions in flight, the deadline flag, timers).
#[derive(Debug, Deserialize, Eq, PartialEq)]
struct Observed {
    deliveries: BTreeMap<i64, ModelDelivery>,
    couriers: BTreeMap<i64, ModelCourier>,
    offers: BTreeMap<i64, ModelOffer>,
    grants: BTreeSet<(i64, i64)>,
    wakes: BTreeSet<i64>,
    #[serde(rename = "candidateWakes")]
    candidate_wakes: BTreeSet<i64>,
}

/// One trace: a Market of its own over the shared database.
struct World {
    deliveries: BTreeMap<i64, DeliveryId>,
    orders: BTreeMap<i64, OrderId>,
    couriers: BTreeMap<i64, CourierId>,
    offers: BTreeMap<i64, DeliveryOfferId>,
    market: MarketId,
    assignment: RecordingAssignment,
    relationships: RecordingRelationships,
    locator: StaticCourierLocator,
    calls_seen: usize,
    wakes: BTreeSet<i64>,
    candidate_wakes: BTreeSet<i64>,
    /// The use case of a transaction in flight runs at its commit.
    picked: Option<i64>,
    closing: Option<(i64, bool)>,
    /// The model revokes the grant a step before the commit.
    revoked: Option<(i64, i64)>,
    offering: Option<(i64, i64)>,
    accepting: Option<i64>,
}

struct AssignmentDriver {
    store: Arc<Store>,
    database_uri: String,
    clock: ManualClock,
    world: Option<World>,
}

fn unique(prefix: &str) -> String {
    static NEXT: AtomicUsize = AtomicUsize::new(0);
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos();
    format!(
        "{prefix}{}{}",
        nanos % 1_000_000_000_000,
        NEXT.fetch_add(1, Ordering::Relaxed)
    )
}

fn key_of<V: PartialEq>(ids: &BTreeMap<i64, V>, id: &V) -> Result<i64> {
    ids.iter()
        .find(|(_, value)| *value == id)
        .map(|(key, _)| *key)
        .ok_or_else(|| anyhow!("id unknown to the model"))
}

fn service() -> Subject {
    Subject::Service("model".to_owned())
}

impl AssignmentDriver {
    fn new() -> Self {
        let database = support::database();
        Self {
            database_uri: database.uri.clone(),
            store: Arc::new(
                Store::connect(&database)
                    .expect("connect")
                    .with_outbox_mapper(Arc::new(DeliveryIntegrationEvents)),
            ),
            clock: ManualClock::new(NOW),
            world: None,
        }
    }

    fn world(&self) -> Result<&World> {
        self.world.as_ref().context("init has not run")
    }

    fn world_mut(&mut self) -> Result<&mut World> {
        self.world.as_mut().context("init has not run")
    }

    fn candidates(world: &World, only: Option<i64>) -> Vec<CourierCandidate> {
        world
            .couriers
            .iter()
            .filter(|(key, _)| only.is_none_or(|courier| courier == **key))
            .map(|(_, courier_id)| CourierCandidate {
                courier_id: courier_id.clone(),
                market_id: world.market.clone(),
                within_delivery_zone: true,
                distance_to_pickup_meters: 1_000,
                active_assignments: 0,
            })
            .collect()
    }

    fn init(&mut self) -> Result {
        let prefix = unique("m");
        let market = MarketId::new(&prefix).map_err(|error| anyhow!("{error:?}"))?;
        let mut deliveries = BTreeMap::new();
        let mut orders = BTreeMap::new();
        for key in 1..=3 {
            let delivery_id = DeliveryId::new(format!("{prefix}-delivery-{key}"));
            let order_id = OrderId::new(format!("{prefix}-order-{key}"));
            DeliveryRepository::add(
                &*self.store,
                &Delivery::new(delivery_id.clone(), order_id.clone(), market.clone()),
            )?;
            deliveries.insert(key, delivery_id);
            orders.insert(key, order_id);
        }
        let mut couriers = BTreeMap::new();
        for key in 1..=2 {
            let courier_id = CourierId::new(format!("{prefix}-courier-{key}"));
            CourierRepository::add(
                &*self.store,
                &Courier::new(
                    courier_id.clone(),
                    CourierName::new("Timur").map_err(|error| anyhow!("{error:?}"))?,
                    market.clone(),
                ),
            )?;
            CourierLocationStore::record(
                &*self.store,
                &CourierPosition {
                    courier_id: courier_id.clone(),
                    location: Location::new(52.52, 13.405).map_err(|error| anyhow!("{error:?}"))?,
                    recorded_at_epoch_seconds: 0,
                },
                0,
            )?;
            couriers.insert(key, courier_id);
        }
        let wakes = deliveries.keys().copied().collect();
        let world = World {
            deliveries,
            orders,
            couriers,
            offers: BTreeMap::new(),
            market,
            assignment: RecordingAssignment::new(),
            relationships: RecordingRelationships::new(),
            locator: StaticCourierLocator::new(Vec::new()),
            calls_seen: 0,
            wakes,
            candidate_wakes: BTreeSet::new(),
            picked: None,
            closing: None,
            revoked: None,
            offering: None,
            accepting: None,
        };
        world.locator.set_candidates(Self::candidates(&world, None));
        self.world = Some(world);
        Ok(())
    }

    /// Calls of the use case to the workflow ports become the wakes of the model.
    fn drain_calls(&mut self) -> Result {
        let world = self.world_mut()?;
        let calls = world.assignment.calls();
        for call in &calls[world.calls_seen..] {
            match call {
                AssignmentCall::Start(_) => {}
                AssignmentCall::Wake(delivery_id, _) => {
                    let key = key_of(&world.deliveries, delivery_id)?;
                    world.wakes.insert(key);
                }
                AssignmentCall::CourierAvailable(courier_id) => {
                    let key = key_of(&world.couriers, courier_id)?;
                    world.candidate_wakes.insert(key);
                }
            }
        }
        world.calls_seen = calls.len();
        Ok(())
    }

    fn run_advance(&mut self, d: i64, only: Option<i64>) -> Result {
        let world = self.world()?;
        world.locator.set_candidates(Self::candidates(world, only));
        let output = advance::Handler {
            unit_of_work: &*self.store,
            deliveries: &*self.store,
            couriers: &*self.store,
            offers: &*self.store,
            offers_of: &*self.store,
            outbox: &*self.store,
            locator: &world.locator,
            clock: &self.clock,
            policy: POLICY,
        }
        .execute(advance::Input {
            delivery_id: world.deliveries[&d].as_str().to_owned(),
        })?;
        world.locator.set_candidates(Self::candidates(world, None));
        if let advance::Output::Offered { offer_id, .. } = output {
            let world = self.world_mut()?;
            if key_of(&world.offers, &offer_id).is_err() {
                world.offers.insert(world.offers.len() as i64, offer_id);
            }
        }
        Ok(())
    }

    fn advance_begin(&mut self, d: i64, c: Option<i64>) -> Result {
        let world = self.world_mut()?;
        world.wakes.remove(&d);
        match c {
            // The transaction of the step commits in the next step.
            Some(_) => {
                world.picked = c;
                world.closing = Some((d, false));
                Ok(())
            }
            None => self.run_advance(d, None),
        }
    }

    fn advance_commit(&mut self) -> Result {
        let world = self.world_mut()?;
        let (d, _) = world.closing.take().context("no step in flight")?;
        let picked = world.picked.take();
        self.run_advance(d, picked)
    }

    fn offer_commit(&mut self) -> Result {
        let world = self.world_mut()?;
        let (d, c) = world.offering.take().context("no offer in flight")?;
        let world = self.world()?;
        let output = to_courier::Handler {
            authorization: &StaticAuthorization::allow_all(),
            unit_of_work: &*self.store,
            deliveries: &*self.store,
            couriers: &*self.store,
            offers: &*self.store,
            offers_of: &*self.store,
            outbox: &*self.store,
            clock: &self.clock,
            policy: POLICY,
            assignment: &world.assignment,
        }
        .execute(
            &service(),
            to_courier::Input {
                order_id: world.orders[&d].as_str().to_owned(),
                courier_id: world.couriers[&c].as_str().to_owned(),
            },
        )?;
        let world = self.world_mut()?;
        world
            .offers
            .insert(world.offers.len() as i64, output.offer_id);
        self.drain_calls()
    }

    fn reject(&mut self, o: i64) -> Result {
        let world = self.world()?;
        let offer_id = &world.offers[&o];
        let offer = DeliveryOfferRepository::get(&*self.store, offer_id)?.context("offer")?;
        reject::Handler {
            authorization: &StaticAuthorization::allow_all(),
            unit_of_work: &*self.store,
            offers: &*self.store,
            outbox: &*self.store,
            clock: &self.clock,
            assignment: &world.assignment,
            candidates: &world.assignment,
        }
        .execute(
            &service(),
            reject::Input {
                offer_id: offer_id.as_str().to_owned(),
                courier_id: offer.courier_id().as_str().to_owned(),
            },
        )?;
        self.drain_calls()
    }

    fn accept_commit(&mut self) -> Result {
        let world = self.world_mut()?;
        let o = world.accepting.take().context("no acceptance in flight")?;
        let world = self.world()?;
        let offer_id = &world.offers[&o];
        let offer = DeliveryOfferRepository::get(&*self.store, offer_id)?.context("offer")?;
        accept::Handler {
            authorization: &StaticAuthorization::allow_all(),
            unit_of_work: &*self.store,
            offers: &*self.store,
            offers_of: &*self.store,
            deliveries: &*self.store,
            couriers: &*self.store,
            outbox: &*self.store,
            locator: &world.locator,
            clock: &self.clock,
            policy: POLICY,
            relationships: &world.relationships,
            assignment: &world.assignment,
        }
        .execute(
            &service(),
            accept::Input {
                offer_id: offer_id.as_str().to_owned(),
                courier_id: offer.courier_id().as_str().to_owned(),
                expected_pickup_at_epoch_seconds: None,
            },
        )?;
        self.drain_calls()
    }

    fn release_revoke(&mut self) -> Result {
        let world = self.world_mut()?;
        let (d, _) = world.closing.context("no release in flight")?;
        let delivery_id = world.deliveries[&d].clone();
        let couriers = world.couriers.clone();
        let delivery = DeliveryRepository::get(&*self.store, &delivery_id)?.context("delivery")?;
        let courier = delivery.courier_id().context("assigned courier")?;
        let key = key_of(&couriers, courier)?;
        self.world_mut()?.revoked = Some((d, key));
        Ok(())
    }

    fn release_commit(&mut self) -> Result {
        let world = self.world_mut()?;
        let (d, closing) = world.closing.take().context("no release in flight")?;
        world.revoked = None;
        let world = self.world()?;
        let authorization = StaticAuthorization::allow_all();
        if closing {
            cancel::Handler {
                authorization: &authorization,
                unit_of_work: &*self.store,
                deliveries: &*self.store,
                offers: &*self.store,
                offers_of: &*self.store,
                couriers: &*self.store,
                outbox: &*self.store,
                relationships: &world.relationships,
                assignment: &world.assignment,
                candidates: &world.assignment,
            }
            .execute(
                &service(),
                cancel::Input {
                    order_id: world.orders[&d].as_str().to_owned(),
                    reason: String::new(),
                },
            )?;
        } else {
            reassign_courier::Handler {
                authorization: &authorization,
                unit_of_work: &*self.store,
                deliveries: &*self.store,
                couriers: &*self.store,
                outbox: &*self.store,
                relationships: &world.relationships,
                assignment: &world.assignment,
                candidates: &world.assignment,
            }
            .execute(
                &service(),
                reassign_courier::Input {
                    delivery_id: world.deliveries[&d].as_str().to_owned(),
                },
            )?;
        }
        self.drain_calls()
    }

    fn cancel_waiting(&mut self, d: i64) -> Result {
        let world = self.world()?;
        cancel::Handler {
            authorization: &StaticAuthorization::allow_all(),
            unit_of_work: &*self.store,
            deliveries: &*self.store,
            offers: &*self.store,
            offers_of: &*self.store,
            couriers: &*self.store,
            outbox: &*self.store,
            relationships: &world.relationships,
            assignment: &world.assignment,
            candidates: &world.assignment,
        }
        .execute(
            &service(),
            cancel::Input {
                order_id: world.orders[&d].as_str().to_owned(),
                reason: String::new(),
            },
        )?;
        self.drain_calls()
    }

    fn complete(&mut self, d: i64) -> Result {
        let world = self.world()?;
        let delivery_id = world.deliveries[&d].as_str().to_owned();
        let delivery =
            DeliveryRepository::get(&*self.store, &world.deliveries[&d])?.context("delivery")?;
        let courier_id = delivery
            .courier_id()
            .context("assigned courier")?
            .as_str()
            .to_owned();
        let authorization = StaticAuthorization::allow_all();
        pick_up::Handler {
            authorization: &authorization,
            unit_of_work: &*self.store,
            deliveries: &*self.store,
            outbox: &*self.store,
            clock: &self.clock,
            assignment: &world.assignment,
        }
        .execute(
            &service(),
            pick_up::Input {
                delivery_id: delivery_id.clone(),
                courier_id: courier_id.clone(),
                expected_delivered_at_epoch_seconds: None,
            },
        )?;
        deliver::Handler {
            authorization: &authorization,
            unit_of_work: &*self.store,
            deliveries: &*self.store,
            couriers: &*self.store,
            outbox: &*self.store,
            relationships: &world.relationships,
            candidates: &world.assignment,
        }
        .execute(
            &service(),
            deliver::Input {
                delivery_id,
                courier_id,
            },
        )?;
        self.drain_calls()
    }

    /// The deadline of one offer passes; the clock of the other offers stays.
    fn deadline_passes(&mut self, o: i64) -> Result {
        let offer_id = self.world()?.offers[&o].as_str().to_owned();
        let uri = self.database_uri.clone();
        support::off_runtime(move || {
            tokio::runtime::Builder::new_current_thread()
                .enable_all()
                .build()
                .expect("runtime")
                .block_on(async move {
                    let mut connection = sqlx::PgConnection::connect(&uri).await?;
                    sqlx::query(
                        "UPDATE delivery_offers SET offered_at = $2, expires_at = $3 WHERE id = $1",
                    )
                    .bind(offer_id)
                    .bind(NOW - 2)
                    .bind(NOW - 1)
                    .execute(&mut connection)
                    .await?;
                    Ok::<_, sqlx::Error>(())
                })
        })?;
        Ok(())
    }

    fn timer_fires(&mut self, o: i64) -> Result {
        let world = self.world()?;
        let offer_id = &world.offers[&o];
        let offer = DeliveryOfferRepository::get(&*self.store, offer_id)?.context("offer")?;
        let d = key_of(&world.deliveries, offer.delivery_id())?;
        expire::Handler {
            unit_of_work: &*self.store,
            offers: &*self.store,
            outbox: &*self.store,
            clock: &self.clock,
            candidates: &world.assignment,
        }
        .execute(expire::Input {
            offer_id: offer_id.as_str().to_owned(),
        })?;
        // The workflow takes its next step after the timer.
        self.world_mut()?.wakes.insert(d);
        self.drain_calls()
    }

    fn candidate_wake(&mut self, c: i64) -> Result {
        let world = self.world()?;
        let target = self.store.oldest_waiting_for(&world.couriers[&c])?;
        let key = target
            .map(|delivery_id| key_of(&world.deliveries, &delivery_id))
            .transpose()?;
        let world = self.world_mut()?;
        world.candidate_wakes.remove(&c);
        world.wakes.extend(key);
        Ok(())
    }
}

impl Driver for AssignmentDriver {
    type State = Observed;

    fn config() -> Config {
        Config {
            state: &["s"],
            nondet: &[],
        }
    }

    fn step(&mut self, step: &Step) -> Result {
        switch!(step {
            init => self.init()?,
            reconcile => (),
            deadlinePasses(o) => self.deadline_passes(o)?,
            timerFires(o) => self.timer_fires(o)?,
            reject(o) => self.reject(o)?,
            acceptBegin(o) => self.world_mut()?.accepting = Some(o),
            acceptCommit => self.accept_commit()?,
            advanceBegin(d, c?) => self.advance_begin(d, c)?,
            advanceCommit => self.advance_commit()?,
            offerBegin(d, c) => self.world_mut()?.offering = Some((d, c)),
            offerCommit => self.offer_commit()?,
            releaseBegin(d, closing) => self.world_mut()?.closing = Some((d, closing)),
            releaseRevoke => self.release_revoke()?,
            releaseCommit => self.release_commit()?,
            cancelWaiting(d) => self.cancel_waiting(d)?,
            complete(d) => self.complete(d)?,
            candidateWake(c) => self.candidate_wake(c)?,
        })
    }
}

impl State<AssignmentDriver> for Observed {
    fn from_driver(driver: &AssignmentDriver) -> Result<Self> {
        let world = driver.world()?;
        let store = &*driver.store;
        let courier_key = |courier: Option<&CourierId>| {
            courier.map_or(Ok(NO_COURIER), |id| key_of(&world.couriers, id))
        };
        let mut deliveries = BTreeMap::new();
        for (key, id) in &world.deliveries {
            let delivery = DeliveryRepository::get(store, id)?.context("delivery")?;
            let state = match delivery.state() {
                DeliveryFsmState::Waiting => ModelDeliveryState::Waiting,
                DeliveryFsmState::Assigned => ModelDeliveryState::Assigned,
                other => {
                    if other == DeliveryFsmState::PickedUp {
                        return Err(anyhow!("a step ended in Pickup"));
                    }
                    ModelDeliveryState::Closed
                }
            };
            deliveries.insert(
                *key,
                ModelDelivery {
                    state,
                    courier: courier_key(delivery.courier_id())?,
                },
            );
        }
        let mut couriers = BTreeMap::new();
        for (key, id) in &world.couriers {
            let courier = CourierRepository::get(store, id)?.context("courier")?;
            let status = match courier.status() {
                CourierStatus::Available => ModelCourierStatus::Available,
                CourierStatus::Busy => ModelCourierStatus::Busy,
                CourierStatus::Unavailable => return Err(anyhow!("courier went off line")),
            };
            couriers.insert(*key, ModelCourier { status });
        }
        let mut offers = BTreeMap::new();
        for (key, id) in &world.offers {
            let offer = DeliveryOfferRepository::get(store, id)?.context("offer")?;
            offers.insert(
                *key,
                ModelOffer {
                    delivery: key_of(&world.deliveries, offer.delivery_id())?,
                    courier: courier_key(Some(offer.courier_id()))?,
                    state: match offer.state() {
                        OfferState::Pending => ModelOfferState::Pending,
                        OfferState::Accepted => ModelOfferState::Accepted,
                        OfferState::Rejected => ModelOfferState::Rejected,
                        OfferState::Withdrawn => ModelOfferState::Withdrawn,
                        OfferState::Expired => ModelOfferState::Expired,
                    },
                },
            );
        }
        let mut grants = BTreeSet::new();
        for write in world.relationships.writes() {
            match write {
                RelationshipWrite::AssignCourier(delivery_id, courier_id) => {
                    grants.insert((
                        key_of(&world.deliveries, &delivery_id)?,
                        key_of(&world.couriers, &courier_id)?,
                    ));
                }
                RelationshipWrite::UnassignCourier(delivery_id, courier_id) => {
                    grants.remove(&(
                        key_of(&world.deliveries, &delivery_id)?,
                        key_of(&world.couriers, &courier_id)?,
                    ));
                }
                RelationshipWrite::RegisterCourier(_) => {}
            }
        }
        if let Some(revoked) = &world.revoked {
            grants.remove(revoked);
        }
        Ok(Self {
            deliveries,
            couriers,
            offers,
            grants,
            wakes: world.wakes.clone(),
            candidate_wakes: world.candidate_wakes.clone(),
        })
    }
}

#[quint_run(
    spec = "verification/quint/assignment.qnt",
    step = "sequentialStep",
    max_samples = 100,
    max_steps = 30
)]
#[ignore = "needs quint in PATH: make verify-quint"]
fn traces_of_the_model_replay_on_the_use_cases() -> impl Driver {
    AssignmentDriver::new()
}
