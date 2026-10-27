//! Движение курьера Simulator по Virtual City (FR-662): маршрут — случайный
//! кратчайший путь по улицам, позиция — точка маршрута по доле прошедшего
//! времени до запланированного момента прибытия.

use crate::slices::courier::domains::courier::{Location, LocationError};
use crate::slices::delivery::domains::delivery::{Intersection, VirtualCity};

use super::Randomness;

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
