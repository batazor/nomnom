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
