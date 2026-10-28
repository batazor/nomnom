//! [`Worker`], который вместе с основным worker процесса читает стрим Kafka
//! ([`EventConsumer`]). Lifecycle тот же, что у [`super::OfferExpiryWorker`]:
//! consumer останавливается по сигналу остановки основного worker, начатое
//! сообщение доводится до конца, его offset подтверждается после обработки.

use std::io;

use tokio::sync::watch;

use crate::infrastructure::messaging::{EventConsumer, MessageHandler};
use crate::pkg::lifecycle::Worker;

pub struct ConsumingWorker<W> {
    inner: W,
    consumer: EventConsumer,
    handler: MessageHandler,
}

impl<W: Worker> ConsumingWorker<W> {
    #[must_use]
    pub fn new(inner: W, consumer: EventConsumer, handler: MessageHandler) -> Self {
        Self {
            inner,
            consumer,
            handler,
        }
    }
}

impl<W: Worker> Worker for ConsumingWorker<W> {
    async fn run(self, stop: watch::Receiver<bool>) -> io::Result<()> {
        let consuming = tokio::spawn(self.consumer.run(self.handler, stop.clone()));
        let result = self.inner.run(stop.clone()).await;
        if !*stop.borrow() {
            // Основной worker завершился сам: lifecycle сочтёт это ошибкой.
            consuming.abort();
        }
        let _ = consuming.await;
        result
    }
}
