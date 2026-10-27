//! Движение курьера Simulator по Virtual City (FR-662): маршрут — случайный
//! кратчайший путь по улицам, позиция — точка маршрута по доле прошедшего
//! времени до запланированного момента прибытия. [`Movements`] держит
//! маршруты шагов доставок; расписание действий им не владеет и только
//! сообщает моменты прибытия.

use std::collections::{HashMap, HashSet};

use crate::slices::courier::domains::courier::{CourierId, Location, LocationError};
use crate::slices::delivery::domains::delivery::{DeliveryId, Intersection, VirtualCity};
use crate::slices::simulator::applications::work::{ActiveDeliveryWork, DeliveryStep};

use super::Randomness;

/// Шаг доставки, по которому едет курьер.
type Leg = (DeliveryId, DeliveryStep);

/// Курьер на маршруте шага доставки.
#[derive(Clone, Debug)]
struct Moving {
    courier: CourierId,
    movement: Movement,
}

/// Маршруты курьеров по шагам доставок. Шаг, которого больше нет в работе
/// Simulator, забывается; маршруты живут в памяти процесса.
pub struct Movements {
    city: VirtualCity,
    moving: HashMap<Leg, Moving>,
}

impl Movements {
    #[must_use]
    pub fn new(city: VirtualCity) -> Self {
        Self {
            city,
            moving: HashMap::new(),
        }
    }

    /// Выключенный Simulator никого не ведёт.
    pub fn clear(&mut self) {
        self.moving.clear();
    }

    /// Забывает маршруты исчезнувших шагов и строит маршруты новых шагов
    /// доставок с точками: в `Assigned` — от перекрёстка у сохранённой
    /// позиции курьера (неизвестна — стоит у pickup) к pickup, в `PickedUp` —
    /// от pickup к dropoff. Прибытие — `arrive_at` шага, выход — `now`.
    pub fn follow(
        &mut self,
        deliveries: &[ActiveDeliveryWork],
        arrive_at: impl Fn(&ActiveDeliveryWork) -> i64,
        now: i64,
        random: &mut dyn Randomness,
    ) {
        let legs: HashSet<Leg> = deliveries
            .iter()
            .map(|delivery| (delivery.delivery_id.clone(), delivery.step))
            .collect();
        self.moving.retain(|leg, _| legs.contains(leg));
        for delivery in deliveries {
            let leg = (delivery.delivery_id.clone(), delivery.step);
            if self.moving.contains_key(&leg) {
                continue;
            }
            let Some(points) = delivery.points else {
                continue;
            };
            let (from, to) = match delivery.step {
                DeliveryStep::Pickup => (
                    delivery
                        .courier_location
                        .map_or(points.pickup, |location| self.city.nearest(&location)),
                    points.pickup,
                ),
                DeliveryStep::Handover => (points.pickup, points.dropoff),
            };
            self.moving.insert(
                leg,
                Moving {
                    courier: delivery.courier_id.clone(),
                    movement: Movement {
                        route: route(from, to, random),
                        start_at_epoch_seconds: now,
                        arrive_at_epoch_seconds: arrive_at(delivery),
                    },
                },
            );
        }
    }

    /// Позиции ведомых курьеров в момент `now` (FR-667).
    #[must_use]
    pub fn positions(&self, now: i64) -> Vec<(CourierId, Result<Location, LocationError>)> {
        self.moving
            .values()
            .map(|moving| {
                (
                    moving.courier.clone(),
                    moving.movement.position(&self.city, now),
                )
            })
            .collect()
    }
}

/// Случайный кратчайший путь от `from` до `to`: каждый шаг — квартал по
/// горизонтали или вертикали к цели. Горизонтальный шаг выбирается с
/// вероятностью доли оставшихся горизонтальных шагов, поэтому все
/// кратчайшие пути равновероятны. Первая точка — `from`, последняя — `to`.
pub fn route(
    from: Intersection,
    to: Intersection,
    random: &mut dyn Randomness,
) -> Vec<Intersection> {
    let mut current = from;
    let mut path = vec![current];
    while current != to {
        let horizontal = current.x.abs_diff(to.x);
        let vertical = current.y.abs_diff(to.y);
        let total = usize::try_from(horizontal + vertical).unwrap_or(usize::MAX);
        let step_horizontally = vertical == 0
            || (horizontal > 0
                && random.index(total) < usize::try_from(horizontal).unwrap_or(usize::MAX));
        if step_horizontally {
            current.x = if to.x > current.x {
                current.x + 1
            } else {
                current.x - 1
            };
        } else {
            current.y = if to.y > current.y {
                current.y + 1
            } else {
                current.y - 1
            };
        }
        path.push(current);
    }
    path
}

/// Курьер на маршруте из [`route`] (не пустом): выходит в `start_at`,
/// прибывает в `arrive_at`.
#[derive(Clone, Debug, PartialEq)]
pub struct Movement {
    pub route: Vec<Intersection>,
    pub start_at_epoch_seconds: i64,
    pub arrive_at_epoch_seconds: i64,
}

impl Movement {
    /// Позиция в момент `now`: до выхода — начало, после прибытия — конец,
    /// между ними — точка на доле длины маршрута.
    pub fn position(&self, city: &VirtualCity, now: i64) -> Result<Location, LocationError> {
        let steps = self.route.len() - 1;
        if steps == 0 {
            return city.location(self.route[0]);
        }
        let duration = self.arrive_at_epoch_seconds - self.start_at_epoch_seconds;
        let elapsed = now - self.start_at_epoch_seconds;
        let fraction = if duration <= 0 {
            1.0
        } else {
            (elapsed as f64 / duration as f64).clamp(0.0, 1.0)
        };
        let progress = fraction * steps as f64;
        // `progress` within `0..=steps`; the last segment ends the route.
        #[allow(clippy::cast_possible_truncation, clippy::cast_sign_loss)]
        let index = (progress.floor() as usize).min(steps - 1);
        let (from, to) = (self.route[index], self.route[index + 1]);
        let along = progress - index as f64;
        city.point(
            f64::from(from.x) + (f64::from(to.x) - f64::from(from.x)) * along,
            f64::from(from.y) + (f64::from(to.y) - f64::from(from.y)) * along,
        )
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Шаги по очереди: index всегда `0` — горизонтально, пока можно.
    struct First;

    impl Randomness for First {
        fn delay(
            &mut self,
            min: std::time::Duration,
            _max: std::time::Duration,
        ) -> std::time::Duration {
            min
        }

        fn percent(&mut self) -> u32 {
            0
        }

        fn index(&mut self, _len: usize) -> usize {
            0
        }
    }

    fn city() -> VirtualCity {
        VirtualCity::new(52.515, 13.385, 12, 150.0).expect("city")
    }

    fn work(step: DeliveryStep, points: bool) -> ActiveDeliveryWork {
        ActiveDeliveryWork {
            delivery_id: DeliveryId::new("d-1"),
            courier_id: CourierId::new("c-1"),
            step,
            points: points.then_some(
                crate::slices::simulator::applications::work::DeliveryPoints {
                    pickup: Intersection { x: 4, y: 0 },
                    dropoff: Intersection { x: 4, y: 6 },
                },
            ),
            courier_location: Some(city().location(Intersection { x: 0, y: 0 }).expect("start")),
        }
    }

    /// FR-662: a leg starts once, the next step starts at pickup, a gone
    /// delivery and one without points do not move.
    #[test]
    fn movements_follow_the_legs_of_the_work() {
        let city = city();
        let mut movements = Movements::new(city);

        movements.follow(
            &[work(DeliveryStep::Pickup, true)],
            |_| 1_120,
            1_000,
            &mut First,
        );
        assert_eq!(
            movements.positions(1_060),
            vec![(
                CourierId::new("c-1"),
                city.location(Intersection { x: 2, y: 0 })
            )]
        );

        // Seen again: the route stays, it does not restart from now.
        movements.follow(
            &[work(DeliveryStep::Pickup, true)],
            |_| 9_999,
            1_060,
            &mut First,
        );
        assert_eq!(
            movements.positions(1_120),
            vec![(
                CourierId::new("c-1"),
                city.location(Intersection { x: 4, y: 0 })
            )]
        );

        movements.follow(
            &[work(DeliveryStep::Handover, true)],
            |_| 1_180,
            1_120,
            &mut First,
        );
        assert_eq!(
            movements.positions(1_120),
            vec![(
                CourierId::new("c-1"),
                city.location(Intersection { x: 4, y: 0 })
            )]
        );

        movements.follow(&[], |_| 0, 1_130, &mut First);
        assert!(movements.positions(1_130).is_empty());

        movements.follow(
            &[work(DeliveryStep::Pickup, false)],
            |_| 1_200,
            1_140,
            &mut First,
        );
        assert!(movements.positions(1_150).is_empty());
    }

    /// AC-882
    #[test]
    fn route_is_a_shortest_path_along_the_streets() {
        let from = Intersection { x: 5, y: 1 };
        let to = Intersection { x: 2, y: 4 };
        let mut random = crate::di::SystemRandomness::seeded();

        let path = route(from, to, &mut random);

        assert_eq!(path.first(), Some(&from));
        assert_eq!(path.last(), Some(&to));
        assert_eq!(
            path.len() - 1,
            usize::try_from(from.blocks_to(to)).expect("blocks")
        );
        for pair in path.windows(2) {
            assert_eq!(pair[0].blocks_to(pair[1]), 1, "one block per step");
            assert!(
                pair[1].blocks_to(to) < pair[0].blocks_to(to),
                "towards the target"
            );
        }
    }

    #[test]
    fn route_to_the_same_intersection_is_a_single_point() {
        let at = Intersection { x: 3, y: 3 };
        assert_eq!(route(at, at, &mut First), vec![at]);
    }

    /// AC-880, AC-881
    #[test]
    fn position_follows_the_share_of_elapsed_time() {
        let city = city();
        let movement = Movement {
            route: route(
                Intersection { x: 0, y: 0 },
                Intersection { x: 4, y: 0 },
                &mut First,
            ),
            start_at_epoch_seconds: 1_000,
            arrive_at_epoch_seconds: 1_120,
        };

        assert_eq!(
            movement.position(&city, 1_060),
            city.location(Intersection { x: 2, y: 0 })
        );
        assert_eq!(movement.position(&city, 1_015), city.point(0.5, 0.0));
        assert_eq!(
            movement.position(&city, 1_200),
            city.location(Intersection { x: 4, y: 0 })
        );
        assert_eq!(
            movement.position(&city, 900),
            city.location(Intersection { x: 0, y: 0 })
        );
    }
}
