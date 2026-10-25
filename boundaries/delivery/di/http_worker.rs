//! [`Worker`] бизнес-порта: HTTP-каркас, который пока отвечает 404 на любой
//! запрос, потому что транспортные handlers ещё не подключены.

use std::io;
use std::net::SocketAddr;

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};
use tokio::sync::watch;
use tokio::task::JoinSet;

use crate::pkg::lifecycle::{Worker, wait_for_stop};

pub struct HttpWorker {
    listener: TcpListener,
}

impl HttpWorker {
    /// Открывает бизнес-порт; ошибка прерывает startup до ready.
    pub async fn bind(addr: SocketAddr) -> io::Result<Self> {
        let listener = TcpListener::bind(addr).await?;
        tracing::info!(operation = "http.listen", address = %listener.local_addr()?, "delivery listening");
        Ok(Self { listener })
    }

    pub fn local_addr(&self) -> io::Result<SocketAddr> {
        self.listener.local_addr()
    }
}

impl Worker for HttpWorker {
    /// Принимает соединения до сигнала остановки, затем закрывает listener и
    /// дожидается начатых соединений. Соединения принадлежат `JoinSet`: если
    /// lifecycle прерывает worker по deadline, они прерываются вместе с ним.
    async fn run(self, mut stop: watch::Receiver<bool>) -> io::Result<()> {
        let Self { listener } = self;
        let mut connections = JoinSet::new();

        loop {
            tokio::select! {
                accepted = listener.accept() => match accepted {
                    Ok((stream, _)) => {
                        connections.spawn(async move {
                            if let Err(error) = handle(stream).await {
                                tracing::warn!(operation = "http.connection", error.message = %error, "connection failed");
                            }
                        });
                    }
                    Err(error) => {
                        tracing::warn!(operation = "http.accept", error.message = %error, "accept failed");
                    }
                },
                Some(_) = connections.join_next() => {}
                () = wait_for_stop(&mut stop) => break,
            }
        }

        drop(listener);
        while connections.join_next().await.is_some() {}

        Ok(())
    }
}

async fn handle(mut stream: TcpStream) -> io::Result<()> {
    let mut buffer = [0; 1024];
    let bytes_read = stream.read(&mut buffer).await?;
    let _request = String::from_utf8_lossy(&buffer[..bytes_read]);
    let (status, body) = ("404 Not Found", "Not Found\n");

    let response = format!(
        "HTTP/1.1 {status}\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
        body.len()
    );
    stream.write_all(response.as_bytes()).await?;

    Ok(())
}

#[cfg(test)]
mod tests {
    use std::time::Duration;

    use super::*;

    #[tokio::test]
    async fn drains_in_flight_connection_after_stop() {
        let worker = HttpWorker::bind(([127, 0, 0, 1], 0).into())
            .await
            .expect("bind");
        let addr = worker.local_addr().expect("addr");
        let (stop_tx, stop_rx) = watch::channel(false);
        let run = tokio::spawn(worker.run(stop_rx));

        // Медленный клиент: соединение принято, запрос ещё не отправлен.
        let mut client = TcpStream::connect(addr).await.expect("connect");
        tokio::time::sleep(Duration::from_millis(100)).await;

        stop_tx.send(true).expect("send stop");
        tokio::time::sleep(Duration::from_millis(100)).await;
        assert!(!run.is_finished(), "worker waits for in-flight connection");
        assert!(
            TcpStream::connect(addr).await.is_err(),
            "listener closed after stop"
        );

        client
            .write_all(b"GET /deliveries HTTP/1.1\r\nHost: test\r\n\r\n")
            .await
            .expect("write request");
        let mut reply = String::new();
        client.read_to_string(&mut reply).await.expect("read reply");
        assert!(reply.starts_with("HTTP/1.1 404 Not Found"));

        tokio::time::timeout(Duration::from_secs(1), run)
            .await
            .expect("drained within deadline")
            .expect("join")
            .expect("graceful stop");
    }
}
