//! [`Worker`], который вместе с основным worker процесса ведёт фоновый цикл
//! Dispatch Deliveries: каждые `interval` проходит ожидающие доставки без
//! pending offer пачками по `batch_size`, продолжая каждую следующую пачку с
//! позиции конца предыдущей, пока пачки полные. Поэтому доставки без
//! кандидата не загораживают следующие. Lifecycle тот же, что у
//! [`super::OfferExpiryWorker`]: цикл останавливается по сигналу остановки
//! основного worker, начатая пачка доводится до конца, и цикл завершается до
//! конца drain.

use std::io;
use std::sync::Arc;
use std::time::Duration;

use tokio::sync::watch;

use crate::pkg::lifecycle::Worker;
use crate::slices::delivery::applications::dispatch_deliveries::{Input, Output};
use crate::slices::delivery::applications::error::DeliveryApplicationError;

/// Один вызов use case Dispatch Deliveries (одна пачка).
pub type DispatchDeliveries =
    Arc<dyn Fn(Input) -> Result<Output, DeliveryApplicationError> + Send + Sync>;

/// Расписание фонового цикла (`OFFERS__DISPATCH_*`).
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct DispatchSchedule {
    pub interval: Duration,
    pub batch_size: usize,
}

pub struct DispatchWorker<W> {
    inner: W,
    dispatch: DispatchDeliveries,
    schedule: DispatchSchedule,
}

impl<W: Worker> DispatchWorker<W> {
    #[must_use]
    pub fn new(inner: W, dispatch: DispatchDeliveries, schedule: DispatchSchedule) -> Self {
        Self {
            inner,
            dispatch,
            schedule,
        }
    }
}

impl<W: Worker> Worker for DispatchWorker<W> {
    async fn run(self, stop: watch::Receiver<bool>) -> io::Result<()> {
        let dispatching = tokio::spawn(run(self.dispatch, self.schedule, stop.clone()));
        let result = self.inner.run(stop.clone()).await;
        if !*stop.borrow() {
            // Основной worker завершился сам: lifecycle сочтёт это ошибкой.
            dispatching.abort();
        }
        let _ = dispatching.await;
        result
    }
}

/// Проходы до `stop == true`. Проход идёт на blocking thread (use cases и
/// хранилище синхронны) и читает пачки, пока они полные и не пришёл сигнал
/// остановки; ошибка пачки пишется в stderr и прерывает только этот проход.
pub async fn run(
    dispatch: DispatchDeliveries,
    schedule: DispatchSchedule,
    mut stop: watch::Receiver<bool>,
) {
    loop {
        tokio::select! {
            () = tokio::time::sleep(schedule.interval) => {}
            _ = stop.wait_for(|stopped| *stopped) => return,
        }
        let watcher = stop.clone();
        let dispatch = Arc::clone(&dispatch);
        let pass =
            tokio::task::spawn_blocking(move || pass(&*dispatch, schedule.batch_size, &watcher));
        if let Err(error) = pass.await {
            eprintln!("dispatch deliveries: pass aborted: {error}");
            return;
        }
        if *stop.borrow() {
            return;
        }
    }
}

fn pass(
    dispatch: &(dyn Fn(Input) -> Result<Output, DeliveryApplicationError> + Send + Sync),
    batch_size: usize,
    stop: &watch::Receiver<bool>,
) {
    let (mut offered, mut waiting, mut skipped) = (0, 0, 0);
    let mut after = None;
    while !*stop.borrow() {
        match dispatch(Input { batch_size, after }) {
            Ok(output) => {
                offered += output.offered.len();
                waiting += output.without_candidate.len();
                skipped += output.skipped.len();
                match output.next {
                    Some(next) => after = Some(next),
                    None => break,
                }
            }
            Err(error) => {
                eprintln!("dispatch deliveries: batch failed: {error}");
                break;
            }
        }
    }
    if offered > 0 || skipped > 0 {
        println!("dispatch deliveries: offered={offered} waiting={waiting} skipped={skipped}");
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Mutex;
    use std::sync::atomic::{AtomicUsize, Ordering};

    use super::*;
    use crate::slices::delivery::domains::delivery::{DeliveryId, DeliveryPosition};

    const SCHEDULE: DispatchSchedule = DispatchSchedule {
        interval: Duration::from_millis(5),
        batch_size: 2,
    };

    struct IdleWorker;

    impl Worker for IdleWorker {
        async fn run(self, mut stop: watch::Receiver<bool>) -> io::Result<()> {
            crate::pkg::lifecycle::wait_for_stop(&mut stop).await;
            Ok(())
        }
    }

    fn position(id: &str) -> DeliveryPosition {
        DeliveryPosition {
            created_at_epoch_seconds: 1_000,
            delivery_id: DeliveryId::new(id),
        }
    }

    /// Full batch of deliveries nobody can take yet, ending at `last`.
    fn full_batch(first: &str, last: &str) -> Output {
        Output {
            without_candidate: vec![DeliveryId::new(first), DeliveryId::new(last)],
            next: Some(position(last)),
            ..Output::default()
        }
    }

    #[tokio::test]
    async fn pass_continues_after_the_previous_batch_then_waits_and_stops() {
        let batches = Arc::new(Mutex::new(vec![
            Output::default(),
            full_batch("c", "d"),
            full_batch("a", "b"),
        ]));
        let inputs = Arc::new(Mutex::new(Vec::new()));
        let (seen_batches, seen_inputs) = (Arc::clone(&batches), Arc::clone(&inputs));
        let dispatch: DispatchDeliveries = Arc::new(move |input| {
            assert_eq!(input.batch_size, 2);
            seen_inputs.lock().expect("inputs").push(input.after);
            Ok(seen_batches
                .lock()
                .expect("batches")
                .pop()
                .unwrap_or_default())
        });
        let (sender, receiver) = watch::channel(false);
        let worker =
            tokio::spawn(DispatchWorker::new(IdleWorker, dispatch, SCHEDULE).run(receiver));

        while inputs.lock().expect("inputs").len() < 4 {
            tokio::time::sleep(Duration::from_millis(1)).await;
        }
        sender.send(true).expect("stop");
        tokio::time::timeout(Duration::from_secs(5), worker)
            .await
            .expect("stops promptly")
            .expect("joined")
            .expect("worker ok");
        // The first pass read three batches, each after the previous one;
        // the next pass starts from the beginning again.
        let inputs = inputs.lock().expect("inputs");
        assert_eq!(
            inputs[..4],
            [None, Some(position("b")), Some(position("d")), None]
        );
    }

    #[tokio::test]
    async fn failed_batch_does_not_stop_the_loop() {
        let calls = Arc::new(AtomicUsize::new(0));
        let seen = Arc::clone(&calls);
        let dispatch: DispatchDeliveries = Arc::new(move |_| {
            seen.fetch_add(1, Ordering::SeqCst);
            Err(DeliveryApplicationError::DependencyUnavailable(
                "db down".to_owned(),
            ))
        });
        let (sender, receiver) = watch::channel(false);
        let loop_task = tokio::spawn(run(dispatch, SCHEDULE, receiver));

        while calls.load(Ordering::SeqCst) < 2 {
            tokio::time::sleep(Duration::from_millis(1)).await;
        }
        sender.send(true).expect("stop");
        tokio::time::timeout(Duration::from_secs(5), loop_task)
            .await
            .expect("stops promptly")
            .expect("joined");
    }

    #[tokio::test]
    async fn stop_before_the_first_pass_runs_nothing() {
        let calls = Arc::new(AtomicUsize::new(0));
        let seen = Arc::clone(&calls);
        let dispatch: DispatchDeliveries = Arc::new(move |_| {
            seen.fetch_add(1, Ordering::SeqCst);
            Ok(Output::default())
        });
        let (sender, receiver) = watch::channel(true);
        run(
            dispatch,
            DispatchSchedule {
                interval: Duration::from_secs(60),
                batch_size: 1,
            },
            receiver,
        )
        .await;
        drop(sender);
        assert_eq!(calls.load(Ordering::SeqCst), 0);
    }
}
