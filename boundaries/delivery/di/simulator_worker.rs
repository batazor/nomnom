//! [`Worker`], который вместе с основным worker процесса ведёт демо Simulator
//! курьеров (FR-605). Каждые `interval` проход читает переключатель: выключенный
//! Simulator сбрасывает расписание и ничего не делает (AC-764). Включённый
//! находит pending offers всех курьеров и доставки в `Assigned`/`PickedUp`,
//! для новой сущности назначает момент действия `firstSeen + random[MIN, MAX]`
//! и, когда он наступил, выполняет одно действие настоящим use case от имени
//! курьера offer или доставки (`Subject::User(courier_id)`):
//!
//! - pending offer — accept 85%, reject 15%;
//! - `Assigned` — confirm pickup 95%, failure 5%;
//! - `PickedUp` — confirm delivery 95%, failure 5%.
//!
//! Сроки — запланированные моменты (FR-610): принимая offer, Simulator сразу
//! выбирает задержку Pickup, передаёт `expected_pickup_at = now + delay` и
//! ставит Pickup доставки в расписание на этот момент; подтверждая Pickup —
//! так же выбирает задержку вручения, передаёт `expected_delivered_at` и
//! ставит вручение на него. Доставку, которую принял не Simulator, он
//! планирует от первого прохода, как раньше. Задержка срока округляется до
//! целых секунд вверх: срок всегда в будущем.
//!
//! Offer живёт `offer_ttl` (60s) меньше `MAX_DELAY`, поэтому верхняя граница
//! задержки offer обрезается до `expiresAt − interval`: действие успевает до
//! истечения с учётом шага опроса. Если и `MIN_DELAY` уже не успевает, offer
//! получает действие на последнем проходе до истечения.
//!
//! Отказ use case (переход уже выполнил человек, offer истёк) пишется в лог,
//! запись расписания удаляется, Simulator продолжает. Lifecycle тот же, что у
//! [`super::ReconcilingWorker`] (ADR 0017): цикл останавливается по сигналу
//! остановки основного worker, начатый проход доводится до конца.
//!
//! Движение (FR-662, FR-667): доставку с точками Virtual City Simulator ведёт
//! по случайному кратчайшему пути — в `Assigned` от перекрёстка у сохранённой
//! позиции курьера к pickup, в `PickedUp` от pickup к dropoff — с прибытием в
//! запланированный момент шага. Каждые `track_interval` ([`Simulator::track`])
//! он передаёт точку маршрута настоящим Update Courier Location от имени
//! курьера; проходы расписания ([`Simulator::tick`]) идут раз в `interval`.
//! Маршруты живут в памяти, как и расписание.

use std::collections::{HashMap, HashSet};
use std::io;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use tokio::sync::watch;

use crate::pkg::authz::Subject;
use crate::pkg::lifecycle::Worker;
use crate::pkg::persistence::RepositoryError;
use crate::pkg::usecase::Clock;
use crate::slices::courier::applications::error::CourierApplicationError;
use crate::slices::courier::applications::update_location;
use crate::slices::courier::domains::courier::{CourierId, Location};
use crate::slices::delivery::applications::error::DeliveryApplicationError;
use crate::slices::delivery::applications::{
    execution::deliver, execution::pick_up, execution::report_failure, offer::accept, offer::reject,
};
use crate::slices::delivery::domains::delivery::{
    DeliveryFailureCode, DeliveryId, DeliveryOfferId, VirtualCity,
};
use crate::slices::simulator::applications::work::DeliveryStep;
use crate::slices::simulator::di::SimulatorStore;

use super::ServiceModule;

mod movement;

use movement::Movements;

/// Веса исходов, проценты первого исхода (FR-605).
const ACCEPT_OFFER_PERCENT: u32 = 85;
const CONFIRM_PICKUP_PERCENT: u32 = 95;
const CONFIRM_DELIVERY_PERCENT: u32 = 95;

/// Причины Delivery Failure, которые выбирает Simulator: коды каталога, не
/// требующие пояснения.
const FAILURE_REASONS: [DeliveryFailureCode; 4] = [
    DeliveryFailureCode::CustomerUnreachable,
    DeliveryFailureCode::AddressNotFound,
    DeliveryFailureCode::NoAccess,
    DeliveryFailureCode::CustomerRefused,
];

/// Расписание Simulator (`SIMULATOR__*`).
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct SimulatorSchedule {
    pub interval: Duration,
    pub min_delay: Duration,
    pub max_delay: Duration,
}

/// Источник случайности Simulator; в тестах — заданная последовательность.
pub trait Randomness: Send {
    /// Равномерно в `[min, max]`.
    fn delay(&mut self, min: Duration, max: Duration) -> Duration;
    /// Равномерно в `0..100`.
    fn percent(&mut self) -> u32;
    /// Равномерно в `0..len`, `len > 0`.
    fn index(&mut self, len: usize) -> usize;
}

/// xorshift64* с seed от системного времени: демо-нагрузке не нужна
/// криптографическая случайность.
pub struct SystemRandomness(u64);

impl SystemRandomness {
    #[must_use]
    pub fn seeded() -> Self {
        let nanos = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_or(0, |elapsed| elapsed.as_nanos());
        // Младшие 64 бита наносекунд; нулевой seed xorshift не допускает.
        #[allow(clippy::cast_possible_truncation)]
        let seed = (nanos as u64) | 1;
        Self(seed)
    }

    fn next(&mut self) -> u64 {
        let mut x = self.0;
        x ^= x >> 12;
        x ^= x << 25;
        x ^= x >> 27;
        self.0 = x;
        x.wrapping_mul(0x2545_F491_4F6C_DD1D)
    }

    fn below(&mut self, bound: u64) -> u64 {
        self.next() % bound
    }
}

impl Randomness for SystemRandomness {
    fn delay(&mut self, min: Duration, max: Duration) -> Duration {
        let span = u64::try_from((max - min).as_millis()).unwrap_or(u64::MAX - 1);
        min + Duration::from_millis(self.below(span + 1))
    }

    fn percent(&mut self) -> u32 {
        u32::try_from(self.below(100)).unwrap_or(0)
    }

    fn index(&mut self, len: usize) -> usize {
        usize::try_from(self.below(u64::try_from(len).unwrap_or(u64::MAX))).unwrap_or(0)
    }
}

/// Действия курьера, которые выполняет Simulator: настоящие use cases слайса
/// `delivery` от имени курьера.
pub trait CourierActions: Send + Sync {
    /// Принимает offer с названным сроком Pickup; возвращает доставку offer.
    fn accept_offer(
        &self,
        courier: &CourierId,
        offer: &DeliveryOfferId,
        expected_pickup_at_epoch_seconds: i64,
    ) -> Result<DeliveryId, DeliveryApplicationError>;
    fn reject_offer(
        &self,
        courier: &CourierId,
        offer: &DeliveryOfferId,
    ) -> Result<(), DeliveryApplicationError>;
    /// Подтверждает Pickup с названным сроком вручения.
    fn confirm_pickup(
        &self,
        courier: &CourierId,
        delivery: &DeliveryId,
        expected_delivered_at_epoch_seconds: i64,
    ) -> Result<(), DeliveryApplicationError>;
    fn confirm_delivery(
        &self,
        courier: &CourierId,
        delivery: &DeliveryId,
    ) -> Result<(), DeliveryApplicationError>;
    fn report_failure(
        &self,
        courier: &CourierId,
        delivery: &DeliveryId,
        reason: DeliveryFailureCode,
    ) -> Result<(), DeliveryApplicationError>;
    /// Передаёт позицию курьера (Update Courier Location).
    fn update_location(
        &self,
        courier: &CourierId,
        location: Location,
    ) -> Result<(), CourierApplicationError>;
}

/// [`CourierActions`] над use cases процесса.
pub struct UseCaseCourierActions {
    use_cases: Arc<ServiceModule>,
}

impl UseCaseCourierActions {
    #[must_use]
    pub fn new(use_cases: Arc<ServiceModule>) -> Self {
        Self { use_cases }
    }
}

fn courier_subject(courier: &CourierId) -> Subject {
    Subject::User(courier.as_str().to_owned())
}

impl CourierActions for UseCaseCourierActions {
    fn accept_offer(
        &self,
        courier: &CourierId,
        offer: &DeliveryOfferId,
        expected_pickup_at_epoch_seconds: i64,
    ) -> Result<DeliveryId, DeliveryApplicationError> {
        self.use_cases
            .deliveries()
            .accept_delivery_offer(
                &courier_subject(courier),
                accept::Input {
                    offer_id: offer.as_str().to_owned(),
                    courier_id: courier.as_str().to_owned(),
                    expected_pickup_at_epoch_seconds: Some(expected_pickup_at_epoch_seconds),
                },
            )
            .map(|output| output.delivery_id)
    }

    fn reject_offer(
        &self,
        courier: &CourierId,
        offer: &DeliveryOfferId,
    ) -> Result<(), DeliveryApplicationError> {
        self.use_cases
            .deliveries()
            .reject_delivery_offer(
                &courier_subject(courier),
                reject::Input {
                    offer_id: offer.as_str().to_owned(),
                    courier_id: courier.as_str().to_owned(),
                },
            )
            .map(|_| ())
    }

    fn confirm_pickup(
        &self,
        courier: &CourierId,
        delivery: &DeliveryId,
        expected_delivered_at_epoch_seconds: i64,
    ) -> Result<(), DeliveryApplicationError> {
        self.use_cases
            .deliveries()
            .confirm_pickup(
                &courier_subject(courier),
                pick_up::Input {
                    delivery_id: delivery.as_str().to_owned(),
                    courier_id: courier.as_str().to_owned(),
                    expected_delivered_at_epoch_seconds: Some(expected_delivered_at_epoch_seconds),
                },
            )
            .map(|_| ())
    }

    fn confirm_delivery(
        &self,
        courier: &CourierId,
        delivery: &DeliveryId,
    ) -> Result<(), DeliveryApplicationError> {
        self.use_cases
            .deliveries()
            .confirm_delivery(
                &courier_subject(courier),
                deliver::Input {
                    delivery_id: delivery.as_str().to_owned(),
                    courier_id: courier.as_str().to_owned(),
                },
            )
            .map(|_| ())
    }

    fn report_failure(
        &self,
        courier: &CourierId,
        delivery: &DeliveryId,
        reason: DeliveryFailureCode,
    ) -> Result<(), DeliveryApplicationError> {
        self.use_cases
            .deliveries()
            .report_delivery_failure(
                &courier_subject(courier),
                report_failure::Input {
                    delivery_id: delivery.as_str().to_owned(),
                    reporter: report_failure::Reporter::Courier(courier.as_str().to_owned()),
                    reason: reason.as_str().to_owned(),
                    detail: None,
                },
            )
            .map(|_| ())
    }

    fn update_location(
        &self,
        courier: &CourierId,
        location: Location,
    ) -> Result<(), CourierApplicationError> {
        self.use_cases
            .couriers()
            .update_courier_location(
                &courier_subject(courier),
                update_location::Input {
                    courier_id: courier.as_str().to_owned(),
                    latitude: location.latitude(),
                    longitude: location.longitude(),
                },
            )
            .map(|_| ())
    }
}

/// Сущность, ожидающая действия курьера.
#[derive(Clone, Debug, Eq, Hash, PartialEq)]
enum Work {
    Offer(DeliveryOfferId),
    Delivery(DeliveryId, DeliveryStep),
}

#[derive(Clone, Debug)]
struct Due {
    courier: CourierId,
    at_epoch_seconds: i64,
}

/// Расписание и выбор действий Simulator; один проход — [`Self::tick`].
pub struct Simulator {
    store: Arc<dyn SimulatorStore>,
    actions: Arc<dyn CourierActions>,
    clock: Arc<dyn Clock + Send + Sync>,
    random: Box<dyn Randomness>,
    schedule: SimulatorSchedule,
    due: HashMap<Work, Due>,
    movements: Movements,
}

fn seconds(duration: Duration) -> i64 {
    i64::try_from(duration.as_secs()).unwrap_or(i64::MAX)
}

/// Целые секунды с округлением вверх: срок `now + delay` позже `now`.
fn seconds_up(duration: Duration) -> i64 {
    let whole = seconds(duration);
    if duration.subsec_nanos() > 0 {
        whole.saturating_add(1)
    } else {
        whole
    }
}

impl Simulator {
    #[must_use]
    pub fn new(
        store: Arc<dyn SimulatorStore>,
        actions: Arc<dyn CourierActions>,
        clock: Arc<dyn Clock + Send + Sync>,
        random: Box<dyn Randomness>,
        schedule: SimulatorSchedule,
        city: VirtualCity,
    ) -> Self {
        Self {
            store,
            actions,
            clock,
            random,
            schedule,
            due: HashMap::new(),
            movements: Movements::new(city),
        }
    }

    /// Один проход: обновить расписание по текущей работе и выполнить
    /// наступившие действия.
    pub fn tick(&mut self) {
        match self.store.enabled() {
            Ok(true) => {}
            Ok(false) => {
                self.due.clear();
                self.movements.clear();
                return;
            }
            Err(error) => {
                tracing::warn!(operation = "simulator", outcome = "failure", %error, "read simulator switch");
                return;
            }
        }
        let now = self.clock.now_epoch_seconds();
        if let Err(error) = self.refresh(now) {
            tracing::warn!(operation = "simulator", outcome = "failure", %error, "read simulator work");
            return;
        }
        let ready: Vec<(Work, Due)> = self
            .due
            .iter()
            .filter(|(_, due)| due.at_epoch_seconds <= now)
            .map(|(work, due)| (work.clone(), due.clone()))
            .collect();
        for (work, due) in ready {
            self.due.remove(&work);
            self.act(&work, &due.courier);
        }
    }

    /// Убирает из расписания исчезнувшую работу и назначает новую: момент
    /// действия отсчитывается от первого прохода, увидевшего сущность.
    fn refresh(&mut self, now: i64) -> Result<(), RepositoryError> {
        let offers = self.store.pending_offers()?;
        let deliveries = self.store.active_deliveries()?;
        let seen: HashSet<Work> = offers
            .iter()
            .map(|offer| Work::Offer(offer.offer_id.clone()))
            .chain(
                deliveries
                    .iter()
                    .map(|delivery| Work::Delivery(delivery.delivery_id.clone(), delivery.step)),
            )
            .collect();
        self.due.retain(|work, _| seen.contains(work));
        for offer in offers {
            let work = Work::Offer(offer.offer_id);
            if !self.due.contains_key(&work) {
                let delay = self.offer_delay(now, offer.expires_at_epoch_seconds);
                self.schedule_work(work, offer.courier_id, now, delay);
            }
        }
        for delivery in &deliveries {
            let work = Work::Delivery(delivery.delivery_id.clone(), delivery.step);
            if !self.due.contains_key(&work) {
                let delay = self
                    .random
                    .delay(self.schedule.min_delay, self.schedule.max_delay);
                self.schedule_work(work, delivery.courier_id.clone(), now, delay);
            }
        }
        // Курьер прибывает к запланированному моменту шага.
        let due = &self.due;
        self.movements.follow(
            &deliveries,
            |delivery| {
                due.get(&Work::Delivery(delivery.delivery_id.clone(), delivery.step))
                    .map_or(now, |due| due.at_epoch_seconds)
            },
            now,
            &mut *self.random,
        );
        Ok(())
    }

    /// Передаёт текущие точки маршрутов (FR-667). Маршруты есть, только пока
    /// Simulator включён: выключенный проход их сбрасывает.
    pub fn track(&mut self) {
        let now = self.clock.now_epoch_seconds();
        for (courier, position) in self.movements.positions(now) {
            let result = position
                .map_err(|error| error.to_string())
                .and_then(|location| {
                    self.actions
                        .update_location(&courier, location)
                        .map_err(|error| error.to_string())
                });
            if let Err(error) = result {
                tracing::warn!(
                    operation = "simulator",
                    outcome = "failure",
                    action = "update_courier_location",
                    courier_id = courier.as_str(),
                    %error,
                    "simulated courier position rejected"
                );
            }
        }
    }

    fn schedule_work(&mut self, work: Work, courier: CourierId, now: i64, delay: Duration) {
        self.plan(work, courier, now.saturating_add(seconds(delay)));
    }

    fn plan(&mut self, work: Work, courier: CourierId, at_epoch_seconds: i64) {
        self.due.insert(
            work,
            Due {
                courier,
                at_epoch_seconds,
            },
        );
    }

    /// Запланированный момент следующего шага: `now + random[MIN, MAX]`.
    fn planned_at(&mut self) -> i64 {
        let now = self.clock.now_epoch_seconds();
        let delay = self
            .random
            .delay(self.schedule.min_delay, self.schedule.max_delay);
        now.saturating_add(seconds_up(delay))
    }

    /// Принимает offer со сроком Pickup и ставит Pickup на этот срок.
    fn accept(
        &mut self,
        courier: &CourierId,
        offer: &DeliveryOfferId,
    ) -> Result<(), DeliveryApplicationError> {
        let pickup_at = self.planned_at();
        let delivery = self.actions.accept_offer(courier, offer, pickup_at)?;
        self.plan(
            Work::Delivery(delivery, DeliveryStep::Pickup),
            courier.clone(),
            pickup_at,
        );
        Ok(())
    }

    /// Подтверждает Pickup со сроком вручения и ставит вручение на этот срок.
    fn pick_up(
        &mut self,
        courier: &CourierId,
        delivery: &DeliveryId,
    ) -> Result<(), DeliveryApplicationError> {
        let handover_at = self.planned_at();
        self.actions
            .confirm_pickup(courier, delivery, handover_at)?;
        self.plan(
            Work::Delivery(delivery.clone(), DeliveryStep::Handover),
            courier.clone(),
            handover_at,
        );
        Ok(())
    }

    /// Задержка offer в `[MIN, MAX]`, обрезанная до `expiresAt − interval`.
    fn offer_delay(&mut self, now: i64, expires_at: i64) -> Duration {
        let left = expires_at
            .saturating_sub(now)
            .saturating_sub(seconds(self.schedule.interval));
        let left = Duration::from_secs(u64::try_from(left).unwrap_or(0));
        if left < self.schedule.min_delay {
            return left;
        }
        self.random
            .delay(self.schedule.min_delay, self.schedule.max_delay.min(left))
    }

    fn act(&mut self, work: &Work, courier: &CourierId) {
        let (action, result) = match work {
            Work::Offer(offer) => {
                if self.random.percent() < ACCEPT_OFFER_PERCENT {
                    ("accept_delivery_offer", self.accept(courier, offer))
                } else {
                    (
                        "reject_delivery_offer",
                        self.actions.reject_offer(courier, offer),
                    )
                }
            }
            Work::Delivery(delivery, DeliveryStep::Pickup) => {
                if self.random.percent() < CONFIRM_PICKUP_PERCENT {
                    ("confirm_pickup", self.pick_up(courier, delivery))
                } else {
                    let reason = self.failure_reason();
                    (
                        "report_delivery_failure",
                        self.actions.report_failure(courier, delivery, reason),
                    )
                }
            }
            Work::Delivery(delivery, DeliveryStep::Handover) => {
                if self.random.percent() < CONFIRM_DELIVERY_PERCENT {
                    (
                        "confirm_delivery",
                        self.actions.confirm_delivery(courier, delivery),
                    )
                } else {
                    let reason = self.failure_reason();
                    (
                        "report_delivery_failure",
                        self.actions.report_failure(courier, delivery, reason),
                    )
                }
            }
        };
        match result {
            Ok(()) => tracing::info!(
                operation = "simulator",
                outcome = "success",
                action,
                courier_id = courier.as_str(),
                "simulated courier action"
            ),
            Err(error) => tracing::warn!(
                operation = "simulator",
                outcome = "failure",
                action,
                courier_id = courier.as_str(),
                %error,
                "simulated courier action rejected"
            ),
        }
    }

    fn failure_reason(&mut self) -> DeliveryFailureCode {
        FAILURE_REASONS[self.random.index(FAILURE_REASONS.len())]
    }
}

pub struct SimulatorWorker<W> {
    inner: W,
    simulator: Simulator,
    interval: Duration,
    track_interval: Duration,
}

impl<W: Worker> SimulatorWorker<W> {
    /// `track_interval` не больше `interval` (FR-667).
    #[must_use]
    pub fn new(
        inner: W,
        simulator: Simulator,
        interval: Duration,
        track_interval: Duration,
    ) -> Self {
        Self {
            inner,
            simulator,
            interval,
            track_interval,
        }
    }
}

impl<W: Worker> Worker for SimulatorWorker<W> {
    async fn run(self, stop: watch::Receiver<bool>) -> io::Result<()> {
        let simulating = tokio::spawn(run(
            self.simulator,
            self.interval,
            self.track_interval,
            stop.clone(),
        ));
        let result = self.inner.run(stop.clone()).await;
        if !*stop.borrow() {
            // Основной worker завершился сам: lifecycle сочтёт это ошибкой.
            simulating.abort();
        }
        let _ = simulating.await;
        result
    }
}

/// Шаги до `stop == true` каждые `track_interval`: на каждом шаге
/// позиции движущихся курьеров, раз в `interval` — сначала проход
/// расписания. Шаг идёт на blocking thread (use cases и хранилище синхронны).
pub async fn run(
    mut simulator: Simulator,
    interval: Duration,
    track_interval: Duration,
    mut stop: watch::Receiver<bool>,
) {
    let steps_per_pass = (interval.as_millis() / track_interval.as_millis().max(1)).max(1);
    let mut step: u128 = 0;
    loop {
        tokio::select! {
            () = tokio::time::sleep(track_interval) => {}
            _ = stop.wait_for(|stopped| *stopped) => return,
        }
        let pass = step.is_multiple_of(steps_per_pass);
        step = step.wrapping_add(1);
        let pass = tokio::task::spawn_blocking(move || {
            if pass {
                simulator.tick();
            }
            simulator.track();
            simulator
        });
        match pass.await {
            Ok(next) => simulator = next,
            Err(error) => {
                tracing::error!(operation = "simulator", outcome = "failure", %error, "simulator pass aborted");
                return;
            }
        }
        if *stop.borrow() {
            return;
        }
    }
}

#[cfg(test)]
mod tests;
