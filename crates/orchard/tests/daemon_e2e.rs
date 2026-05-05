//! End-to-end test: spin a fake GraphQL daemon on a loopback port and
//! verify the daemon::Client and federated fan-out drive it correctly.
//!
//! The fake daemon answers a hardcoded set of queries; this verifies the
//! client's request shape, error mapping, and federation flatten path
//! against a real socket without depending on the production daemon.

use std::collections::HashMap;
use std::net::SocketAddr;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};

use http::StatusCode;
use http_body_util::{BodyExt, Full};
use hyper::body::{Bytes, Incoming};
use hyper::service::service_fn;
use hyper::{Request, Response};
use hyper_util::rt::{TokioExecutor, TokioIo};
use hyper_util::server::conn::auto;

use orchard::daemon::{Client, fan_out};

/// Spins up a single-handler fake daemon on an ephemeral port.
///
/// Returns the URL pointing at `/graphql` and a request-counter the test can
/// inspect. The server runs on its own tokio runtime and stays alive for
/// the duration of the test (the runtime drops with the join handle).
struct FakeDaemon {
    url: String,
    request_count: Arc<AtomicU64>,
}

fn spawn_fake_daemon(handler: HandlerFn) -> FakeDaemon {
    let request_count = Arc::new(AtomicU64::new(0));
    let counter = Arc::clone(&request_count);
    let (tx, rx) = std::sync::mpsc::channel();
    let handler = Arc::new(handler);

    std::thread::spawn(move || {
        let rt = tokio::runtime::Runtime::new().expect("rt");
        rt.block_on(async move {
            let listener = tokio::net::TcpListener::bind(SocketAddr::from(([127, 0, 0, 1], 0)))
                .await
                .expect("bind");
            let port = listener.local_addr().expect("addr").port();
            tx.send(port).expect("send port");
            loop {
                let (stream, _) = match listener.accept().await {
                    Ok(v) => v,
                    Err(_) => break,
                };
                let io = TokioIo::new(stream);
                let counter = Arc::clone(&counter);
                let handler = Arc::clone(&handler);
                tokio::spawn(async move {
                    let svc = service_fn(move |req: Request<Incoming>| {
                        let counter = Arc::clone(&counter);
                        let handler = Arc::clone(&handler);
                        async move { serve_request(req, counter, handler).await }
                    });
                    let _ = auto::Builder::new(TokioExecutor::new())
                        .serve_connection(io, svc)
                        .await;
                });
            }
        });
    });

    let port = rx
        .recv_timeout(std::time::Duration::from_secs(2))
        .expect("port");
    FakeDaemon {
        url: format!("http://127.0.0.1:{port}/graphql"),
        request_count,
    }
}

type HandlerFn = Box<dyn Fn(&str) -> serde_json::Value + Send + Sync + 'static>;

async fn serve_request(
    req: Request<Incoming>,
    counter: Arc<AtomicU64>,
    handler: Arc<HandlerFn>,
) -> Result<Response<Full<Bytes>>, std::convert::Infallible> {
    counter.fetch_add(1, Ordering::SeqCst);
    let body_bytes = match req.into_body().collect().await {
        Ok(c) => c.to_bytes(),
        Err(_) => {
            return Ok(Response::builder()
                .status(StatusCode::BAD_REQUEST)
                .body(Full::new(Bytes::new()))
                .unwrap());
        }
    };
    let parsed: HashMap<String, serde_json::Value> =
        serde_json::from_slice(&body_bytes).unwrap_or_default();
    let query = parsed.get("query").and_then(|v| v.as_str()).unwrap_or("");
    let resp = handler(query);
    let body = serde_json::to_vec(&resp).expect("encode");
    Ok(Response::builder()
        .status(StatusCode::OK)
        .header("content-type", "application/json")
        .body(Full::new(Bytes::from(body)))
        .unwrap())
}

#[test]
fn client_health_round_trip_against_fake_daemon() {
    let fake = spawn_fake_daemon(Box::new(|q: &str| {
        if q.contains("health") {
            serde_json::json!({"data": {"health": {"status": "ok", "uptimeS": 99}}})
        } else {
            serde_json::json!({"errors": [{"message": "unexpected query"}]})
        }
    }));
    let client = Client::for_url(&fake.url).expect("client");
    let h = client.health().expect("health");
    assert_eq!(h.status, "ok");
    assert_eq!(h.uptime_s, 99);
    assert_eq!(fake.request_count.load(Ordering::SeqCst), 1);
}

#[test]
fn fan_out_walks_local_then_peer() {
    // Two fake daemons: "local" returns 2 sessions + 1 peer, "peer" returns 1
    // session. Fan-out should emit both, host-tagged.
    let peer = spawn_fake_daemon(Box::new(|q: &str| {
        if q.contains("tmuxSessions") {
            serde_json::json!({"data": {"tmuxSessions": [
                {"id": "TmuxSession:peer:p1", "name": "p1", "attached": false, "activeAttached": false}
            ]}})
        } else {
            serde_json::json!({"data": null, "errors": [{"message": "unexpected"}]})
        }
    }));
    let peer_url = peer.url.clone();
    // The client constructs peer URL via peer_url(address); pass an http://...
    // address so it bypasses the graphql.* prefix logic.
    let local = spawn_fake_daemon(Box::new(move |q: &str| {
        if q.contains("hosts") {
            serde_json::json!({"data": {"hosts": [
                {"id":"Host:local","hostname":"localbox","address":null,"reachable":true,"peers":[
                    {"id":"Host:peer","hostname":"peerbox","address":&peer_url,"reachable":true,"peers":[]}
                ]}
            ]}})
        } else if q.contains("tmuxSessions") {
            serde_json::json!({"data": {"tmuxSessions": [
                {"id": "TmuxSession:local:l1", "name": "l1", "attached": true, "activeAttached": true},
                {"id": "TmuxSession:local:l2", "name": "l2", "attached": false, "activeAttached": false}
            ]}})
        } else {
            serde_json::json!({"data": null, "errors": [{"message": "unexpected"}]})
        }
    }));
    let client = Client::for_url(&local.url).expect("client");
    let hosts = client.hosts().expect("hosts");
    let fanout = fan_out(&client, &hosts).expect("fan_out");
    assert_eq!(fanout.local_hostname, "localbox");
    assert_eq!(fanout.local_sessions.len(), 2);
    assert_eq!(fanout.peer_results.len(), 1);
    let peer_result = &fanout.peer_results[0];
    assert_eq!(peer_result.hostname, "peerbox");
    assert!(peer_result.sessions.is_ok(), "peer fetch should succeed");
    let peer_sessions = peer_result.sessions.as_ref().unwrap();
    assert_eq!(peer_sessions.len(), 1);
    assert_eq!(peer_sessions[0].name, "p1");

    let flat = fanout.flatten();
    assert_eq!(flat.len(), 3);
    let local_count = flat.iter().filter(|r| r.is_local).count();
    let peer_count = flat.iter().filter(|r| !r.is_local).count();
    assert_eq!(local_count, 2);
    assert_eq!(peer_count, 1);
}

#[test]
fn unreachable_daemon_yields_unreachable_error() {
    // Bind a port and immediately drop the listener — the kernel returns
    // ECONNREFUSED on connect.
    let listener = std::net::TcpListener::bind("127.0.0.1:0").expect("bind");
    let port = listener.local_addr().expect("addr").port();
    drop(listener);
    let url = format!("http://127.0.0.1:{port}/graphql");
    let client = Client::for_url(&url).expect("client");
    let err = client.health().unwrap_err();
    let msg = format!("{err}");
    assert!(
        msg.contains("not reachable") || msg.contains("transport"),
        "expected unreachable/transport, got: {msg}"
    );
}
