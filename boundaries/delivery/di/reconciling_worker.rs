//! [`Worker`], который вместе с основным worker процесса ведёт фоновый цикл
//! reconciliation relationships (FR-445). Цикл останавливается по тому же
//! сигналу остановки, что и основной worker, и завершается до конца drain.

use std::io;

use tokio::sync::watch;

use crate::infrastructure::reconcile::{self, Reconciler, Schedule};
use crate::pkg::lifecycle::Worker;

pub struct ReconcilingWorker<W> {
    inner: W,
    reconcilers: Vec<Reconciler>,
    schedule: Schedule,
}

impl<W: Worker> ReconcilingWorker<W> {
    #[must_use]
    pub fn new(inner: W, reconcilers: Vec<Reconciler>, schedule: Schedule) -> Self {
        Self {
            inner,
            reconcilers,
            schedule,
        }
    }
}

impl<W: Worker> Worker for ReconcilingWorker<W> {
    async fn run(self, stop: watch::Receiver<bool>) -> io::Result<()> {
        let reconciling = tokio::spawn(reconcile::run(
            self.reconcilers,
            self.schedule,
            stop.clone(),
        ));
        let result = self.inner.run(stop.clone()).await;
        if !*stop.borrow() {
            // Основной worker завершился сам: lifecycle сочтёт это ошибкой.
            reconciling.abort();
        }
        let _ = reconciling.await;
        result
    }
}
