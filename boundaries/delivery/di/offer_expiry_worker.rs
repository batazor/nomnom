//! [`Worker`], который вместе с основным worker процесса ведёт фоновый цикл
//! Expire Delivery Offers: каждые `interval` закрывает pending offers с
//! прошедшим `expires_at` пачками по `batch_size`, пока пачки полные.
//! Lifecycle тот же, что у [`super::ReconcilingWorker`]: цикл останавливается
//! по сигналу остановки основного worker, начатая пачка доводится до конца, и
//! цикл завершается до конца drain.

use std::io;
use std::sync::Arc;
use std::time::Duration;

use tokio::sync::watch;

use crate::pkg::lifecycle::Worker;
use crate::slices::delivery::applications::error::DeliveryApplicationError;
use crate::slices::delivery::applications::expire_delivery_offers::{Input, Output};

/// Один вызов use case Expire Delivery Offers (одна пачка).
pub type ExpireOffers =
    Arc<dyn Fn(Input) -> Result<Output, DeliveryApplicationError> + Send + Sync>;

/// Расписание фонового цикла (`OFFERS__*`).
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ExpirySchedule {
    pub interval: Duration,
    pub batch_size: usize,
}

pub struct OfferExpiryWorker<W> {
    inner: W,
    expire: ExpireOffers,
    schedule: ExpirySchedule,
}

impl<W: Worker> OfferExpiryWorker<W> {
    #[must_use]
    pub fn new(inner: W, expire: ExpireOffers, schedule: ExpirySchedule) -> Self {
        Self {
            inner,
            expire,
            schedule,
        }
    }
}

impl<W: Worker> Worker for OfferExpiryWorker<W> {
    async fn run(self, stop: watch::Receiver<bool>) -> io::Result<()> {
        let expiring = tokio::spawn(run(self.expire, self.schedule, stop.clone()));
        let result = self.inner.run(stop.clone()).await;
        if !*stop.borrow() {
            // Основной worker завершился сам: lifecycle сочтёт это ошибкой.
            expiring.abort();
        }
        let _ = expiring.await;
        result
    }
}

/// Проходы до `stop == true`. Проход идёт на blocking thread (use cases и
/// хранилище синхронны) и читает пачки, пока они полные и не пришёл сигнал
/// остановки; ошибка пачки пишется в stderr и прерывает только этот проход.
pub async fn run(expire: ExpireOffers, schedule: ExpirySchedule, mut stop: watch::Receiver<bool>) {
    loop {
        tokio::select! {
            () = tokio::time::sleep(schedule.interval) => {}
            _ = stop.wait_for(|stopped| *stopped) => return,
        }
        let watcher = stop.clone();
        let expire = Arc::clone(&expire);
        let pass =
            tokio::task::spawn_blocking(move || pass(&*expire, schedule.batch_size, &watcher));
        if let Err(error) = pass.await {
            tracing::error!(operation = "expire_delivery_offers", outcome = "error", error.message = %error, "expire delivery offers: pass aborted");
            return;
        }
        if *stop.borrow() {
            return;
        }
    }
}

fn pass(
    expire: &(dyn Fn(Input) -> Result<Output, DeliveryApplicationError> + Send + Sync),
    batch_size: usize,
    stop: &watch::Receiver<bool>,
) {
    let (mut expired, mut skipped) = (0, 0);
    while !*stop.borrow() {
        match expire(Input { batch_size }) {
            Ok(output) => {
                expired += output.expired.len();
                skipped += output.skipped.len();
                // Пачка без единого истёкшего offer не продвигает выборку.
                if !output.more || output.expired.is_empty() {
                    break;
                }
            }
            Err(error) => {
                tracing::error!(operation = "expire_delivery_offers", outcome = "error", error.message = %error, "expire delivery offers: batch failed");
                break;
            }
        }
    }
    if expired > 0 || skipped > 0 {
        tracing::info!(
            operation = "expire_delivery_offers",
            outcome = "success",
            expired,
            skipped,
            "delivery offers expired"
        );
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Mutex;
    use std::sync::atomic::{AtomicUsize, Ordering};

    use super::*;
    use crate::slices::delivery::domains::delivery::DeliveryOfferId;

    const SCHEDULE: ExpirySchedule = ExpirySchedule {
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

    fn full_batch() -> Output {
        Output {
            expired: vec![DeliveryOfferId::new("a"), DeliveryOfferId::new("b")],
            skipped: Vec::new(),
            more: true,
        }
    }

    #[tokio::test]
    async fn drains_full_batches_then_waits_and_stops_on_signal() {
        let batches = Arc::new(Mutex::new(vec![
            Output::default(),
            full_batch(),
            full_batch(),
        ]));
        let calls = Arc::new(AtomicUsize::new(0));
        let (seen_batches, seen_calls) = (Arc::clone(&batches), Arc::clone(&calls));
        let expire: ExpireOffers = Arc::new(move |input| {
            assert_eq!(input.batch_size, 2);
            seen_calls.fetch_add(1, Ordering::SeqCst);
            Ok(seen_batches
                .lock()
                .expect("batches")
                .pop()
                .unwrap_or_default())
        });
        let (sender, receiver) = watch::channel(false);
        let worker =
            tokio::spawn(OfferExpiryWorker::new(IdleWorker, expire, SCHEDULE).run(receiver));

        while calls.load(Ordering::SeqCst) < 4 {
            tokio::time::sleep(Duration::from_millis(1)).await;
        }
        sender.send(true).expect("stop");
        tokio::time::timeout(Duration::from_secs(5), worker)
            .await
            .expect("stops promptly")
            .expect("joined")
            .expect("worker ok");
        // The first pass drained two full batches and the empty one.
        assert!(batches.lock().expect("batches").is_empty());
    }

    #[tokio::test]
    async fn failed_batch_does_not_stop_the_loop() {
        let calls = Arc::new(AtomicUsize::new(0));
        let seen = Arc::clone(&calls);
        let expire: ExpireOffers = Arc::new(move |_| {
            seen.fetch_add(1, Ordering::SeqCst);
            Err(DeliveryApplicationError::DependencyUnavailable(
                "db down".to_owned(),
            ))
        });
        let (sender, receiver) = watch::channel(false);
        let loop_task = tokio::spawn(run(expire, SCHEDULE, receiver));

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
        let expire: ExpireOffers = Arc::new(move |_| {
            seen.fetch_add(1, Ordering::SeqCst);
            Ok(Output::default())
        });
        let (sender, receiver) = watch::channel(true);
        run(
            expire,
            ExpirySchedule {
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
