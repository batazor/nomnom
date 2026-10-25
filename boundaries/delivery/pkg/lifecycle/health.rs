//! Health state экземпляра и технический порт с `/health/*`.
//!
//! Handlers только читают уже вычисленное состояние и никогда не обращаются
//! к зависимостям.

use std::io;
use std::net::SocketAddr;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};
use tokio::sync::watch;
use tokio::task::{JoinHandle, JoinSet};

use super::wait_for_stop;

const REASON_INITIALIZING: &str = "initializing";
const REASON_SHUTTING_DOWN: &str = "shutting_down";

/// Ограничение на чтение заголовков запроса probe.
const READ_TIMEOUT: Duration = Duration::from_secs(5);
/// Probes короткие: закрытие технического порта не должно съедать бюджет
/// `terminationGracePeriodSeconds`.
const CLOSE_TIMEOUT: Duration = Duration::from_secs(1);
/// Максимальный размер заголовков запроса probe.
const MAX_REQUEST_HEAD: usize = 8 * 1024;

/// Состояние экземпляра для startup, liveness и readiness probes.
#[derive(Debug, Default)]
pub struct Health {
    started: AtomicBool,
    shutting_down: AtomicBool,
}

impl Health {
    pub(crate) fn mark_started(&self) {
        self.started.store(true, Ordering::Release);
    }

    pub(crate) fn mark_shutting_down(&self) {
        self.shutting_down.store(true, Ordering::Release);
    }

    /// Worker захватил ресурсы и запущен.
    #[must_use]
    pub fn is_started(&self) -> bool {
        self.started.load(Ordering::Acquire)
    }

    /// Начата последовательность graceful shutdown.
    #[must_use]
    pub fn is_shutting_down(&self) -> bool {
        self.shutting_down.load(Ordering::Acquire)
    }

    /// Может ли экземпляр принимать новую работу.
    #[must_use]
    pub fn ready(&self) -> bool {
        self.is_started() && !self.is_shutting_down()
    }

    fn probe(&self, path: &str) -> Option<Probe> {
        match path {
            "/health/startup" => Some(Probe::from_state(self.is_started(), REASON_INITIALIZING)),
            // Liveness не зависит от внешних систем и остаётся успешной во время shutdown.
            "/health/live" => Some(Probe::Ok),
            "/health/ready" if self.is_shutting_down() => {
                Some(Probe::Unavailable(REASON_SHUTTING_DOWN))
            }
            "/health/ready" => Some(Probe::from_state(self.is_started(), REASON_INITIALIZING)),
            _ => None,
        }
    }
}

enum Probe {
    Ok,
    Unavailable(&'static str),
}

impl Probe {
    fn from_state(ok: bool, reason: &'static str) -> Self {
        if ok {
            Self::Ok
        } else {
            Self::Unavailable(reason)
        }
    }
}

/// HTTP server технического порта. Останавливается последним, чтобы до конца
/// shutdown probes видели live=200 и ready=503.
pub(crate) struct ManagementServer {
    addr: SocketAddr,
    stop: watch::Sender<bool>,
    task: Option<JoinHandle<()>>,
}

impl ManagementServer {
    pub(crate) async fn bind(addr: SocketAddr, health: Arc<Health>) -> io::Result<Self> {
        let listener = TcpListener::bind(addr).await?;
        let addr = listener.local_addr()?;
        let (stop, stop_rx) = watch::channel(false);
        let task = tokio::spawn(serve(listener, health, stop_rx));
        Ok(Self {
            addr,
            stop,
            task: Some(task),
        })
    }

    pub(crate) fn local_addr(&self) -> SocketAddr {
        self.addr
    }

    /// Прекращает приём probes и ждёт начатые не дольше [`CLOSE_TIMEOUT`].
    pub(crate) async fn shutdown(mut self) {
        let _ = self.stop.send(true);
        if let Some(mut task) = self.task.take()
            && tokio::time::timeout(CLOSE_TIMEOUT, &mut task)
                .await
                .is_err()
        {
            task.abort();
            let _ = task.await;
        }
    }
}

impl Drop for ManagementServer {
    fn drop(&mut self) {
        // Lifecycle, который так и не запустили (например, worker не смог
        // открыть порт), не оставляет фоновый server.
        if let Some(task) = self.task.take() {
            task.abort();
        }
    }
}

async fn serve(listener: TcpListener, health: Arc<Health>, mut stop: watch::Receiver<bool>) {
    let mut connections = JoinSet::new();

    loop {
        tokio::select! {
            accepted = listener.accept() => match accepted {
                Ok((stream, _)) => {
                    let health = Arc::clone(&health);
                    connections.spawn(async move {
                        if let Err(error) = handle(stream, &health).await {
                            eprintln!("management connection failed: {error}");
                        }
                    });
                }
                Err(error) => eprintln!("management accept failed: {error}"),
            },
            Some(_) = connections.join_next() => {}
            () = wait_for_stop(&mut stop) => break,
        }
    }

    drop(listener);
    while connections.join_next().await.is_some() {}
}

async fn handle(mut stream: TcpStream, health: &Health) -> io::Result<()> {
    let head = tokio::time::timeout(READ_TIMEOUT, read_head(&mut stream))
        .await
        .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "request head timeout"))??;

    let mut parts = head.lines().next().unwrap_or_default().split_whitespace();
    let method = parts.next().unwrap_or_default();
    let path = parts
        .next()
        .unwrap_or_default()
        .split('?')
        .next()
        .unwrap_or_default();
    let with_body = method != "HEAD";

    let response = match health.probe(path) {
        Some(_) if method != "GET" && method != "HEAD" => response(
            "405 Method Not Allowed",
            "text/plain; charset=utf-8",
            "Method Not Allowed\n",
            "Allow: GET, HEAD\r\n",
            with_body,
        ),
        Some(Probe::Ok) => response(
            "200 OK",
            "application/json",
            r#"{"status":"ok"}"#,
            "",
            with_body,
        ),
        Some(Probe::Unavailable(reason)) => response(
            "503 Service Unavailable",
            "application/json",
            &format!(r#"{{"status":"unavailable","reason":"{reason}"}}"#),
            "",
            with_body,
        ),
        None => response(
            "404 Not Found",
            "text/plain; charset=utf-8",
            "Not Found\n",
            "",
            with_body,
        ),
    };
    stream.write_all(response.as_bytes()).await?;
    stream.shutdown().await
}

/// Читает заголовки запроса целиком: непрочитанные данные при закрытии
/// сокета приводят к RST, и клиент может не получить ответ.
async fn read_head(stream: &mut TcpStream) -> io::Result<String> {
    let mut head = Vec::with_capacity(1024);
    let mut buffer = [0; 1024];
    while !head.windows(4).any(|window| window == b"\r\n\r\n") {
        if head.len() >= MAX_REQUEST_HEAD {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "request head too large",
            ));
        }
        let bytes_read = stream.read(&mut buffer).await?;
        if bytes_read == 0 {
            break;
        }
        head.extend_from_slice(&buffer[..bytes_read]);
    }
    Ok(String::from_utf8_lossy(&head).into_owned())
}

fn response(
    status: &str,
    content_type: &str,
    body: &str,
    extra_headers: &str,
    with_body: bool,
) -> String {
    let body_out = if with_body { body } else { "" };
    format!(
        "HTTP/1.1 {status}\r\nContent-Type: {content_type}\r\nCache-Control: no-store\r\n{extra_headers}Content-Length: {}\r\nConnection: close\r\n\r\n{body_out}",
        body.len()
    )
}

#[cfg(test)]
pub(crate) mod tests {
    use super::*;

    /// Ответ технического порта: статус, заголовки и тело.
    pub(crate) struct Reply {
        pub status: u16,
        pub head: String,
        pub body: String,
    }

    pub(crate) async fn get(addr: SocketAddr, path: &str) -> Reply {
        request(addr, "GET", path).await
    }

    pub(crate) async fn request(addr: SocketAddr, method: &str, path: &str) -> Reply {
        let mut stream = TcpStream::connect(addr).await.expect("connect management");
        stream
            .write_all(format!("{method} {path} HTTP/1.1\r\nHost: test\r\n\r\n").as_bytes())
            .await
            .expect("write request");
        let mut raw = String::new();
        stream.read_to_string(&mut raw).await.expect("read reply");
        let (head, body) = raw.split_once("\r\n\r\n").expect("reply head");
        let status = head
            .split_whitespace()
            .nth(1)
            .and_then(|code| code.parse().ok())
            .expect("status code");
        Reply {
            status,
            head: head.to_owned(),
            body: body.to_owned(),
        }
    }

    async fn serve_health() -> (ManagementServer, Arc<Health>) {
        let health = Arc::new(Health::default());
        let server = ManagementServer::bind(([127, 0, 0, 1], 0).into(), Arc::clone(&health))
            .await
            .expect("bind management");
        (server, health)
    }

    #[tokio::test]
    async fn reports_initializing_before_start() {
        let (server, _health) = serve_health().await;
        let addr = server.local_addr();

        let startup = get(addr, "/health/startup").await;
        assert_eq!(startup.status, 503);
        assert_eq!(
            startup.body,
            r#"{"status":"unavailable","reason":"initializing"}"#
        );
        let ready = get(addr, "/health/ready").await;
        assert_eq!(ready.status, 503);
        assert_eq!(
            ready.body,
            r#"{"status":"unavailable","reason":"initializing"}"#
        );
        assert_eq!(get(addr, "/health/live").await.status, 200);

        server.shutdown().await;
    }

    #[tokio::test]
    async fn follows_state_transitions() {
        let (server, health) = serve_health().await;
        let addr = server.local_addr();

        health.mark_started();
        let ready = get(addr, "/health/ready?verbose=1").await;
        assert_eq!(ready.status, 200);
        assert_eq!(ready.body, r#"{"status":"ok"}"#);
        assert!(ready.head.contains("Content-Type: application/json"));
        assert!(ready.head.contains("Cache-Control: no-store"));
        assert!(ready.head.contains("Connection: close"));
        assert_eq!(get(addr, "/health/startup").await.status, 200);

        health.mark_shutting_down();
        let ready = get(addr, "/health/ready").await;
        assert_eq!(ready.status, 503);
        assert_eq!(
            ready.body,
            r#"{"status":"unavailable","reason":"shutting_down"}"#
        );
        let live = get(addr, "/health/live").await;
        assert_eq!(live.status, 200);
        assert_eq!(live.body, r#"{"status":"ok"}"#);

        server.shutdown().await;
    }

    #[tokio::test]
    async fn rejects_unknown_paths_and_methods() {
        let (server, _health) = serve_health().await;
        let addr = server.local_addr();

        assert_eq!(get(addr, "/health").await.status, 404);
        assert_eq!(get(addr, "/").await.status, 404);
        let post = request(addr, "POST", "/health/live").await;
        assert_eq!(post.status, 405);
        assert!(post.head.contains("Allow: GET, HEAD"));
        let head = request(addr, "HEAD", "/health/live").await;
        assert_eq!(head.status, 200);
        assert!(head.body.is_empty());

        server.shutdown().await;
        assert!(TcpStream::connect(addr).await.is_err());
    }
}
