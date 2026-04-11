//! Hyper server that drives the webhook handler.
//!
//! Binds a TCP listener on the resolved port, accepts connections with
//! hyper-util's `auto::Builder`, and routes each request through
//! `handler::handle_request`. The server is blocking from the caller's
//! perspective (runs the tokio runtime inline) and returns when it receives
//! SIGINT/SIGTERM.

use crate::webhook::handler::{MAX_BODY_BYTES, WebhookRequest, handle_request};
use anyhow::{Context, Result};
use http::StatusCode;
use http_body_util::{BodyExt, Full, Limited};
use hyper::body::{Bytes, Incoming};
use hyper::service::service_fn;
use hyper::{Request, Response};
use hyper_util::rt::TokioIo;
use hyper_util::server::conn::auto;
use std::collections::HashMap;
use std::net::SocketAddr;
use std::sync::Arc;

/// Runs the webhook server on the given port with the given secret.
///
/// Binds to `127.0.0.1:<port>`. If `port` is 0, the OS assigns an ephemeral
/// port. Prints the startup line to stderr with the actual bound port.
///
/// Blocks until the process receives SIGINT (Ctrl-C).
pub fn run(port: u16, secret: Vec<u8>) -> Result<()> {
    let rt = tokio::runtime::Runtime::new().context("failed to create tokio runtime")?;
    rt.block_on(async_main(port, secret))
}

async fn async_main(port: u16, secret: Vec<u8>) -> Result<()> {
    let addr = SocketAddr::from(([127, 0, 0, 1], port));
    let listener = tokio::net::TcpListener::bind(addr)
        .await
        .with_context(|| format!("failed to bind to {addr}"))?;

    let bound_port = listener.local_addr()?.port();
    let events_path = events_path_for_display();

    eprintln!(
        "orchard webhook-serve: listening on http://127.0.0.1:{bound_port}/webhook (events → {events_path})"
    );

    let secret = Arc::new(secret);

    loop {
        let (stream, _peer) = tokio::select! {
            result = listener.accept() => result?,
            _ = tokio::signal::ctrl_c() => break,
        };

        let secret = Arc::clone(&secret);
        let io = TokioIo::new(stream);

        tokio::spawn(async move {
            let secret = Arc::clone(&secret);
            let svc = service_fn(move |req: Request<Incoming>| {
                let secret = Arc::clone(&secret);
                async move { serve_request(req, secret).await }
            });
            let _ = auto::Builder::new(hyper_util::rt::TokioExecutor::new())
                .serve_connection(io, svc)
                .await;
        });
    }

    Ok(())
}

async fn serve_request(
    req: Request<Incoming>,
    secret: Arc<Vec<u8>>,
) -> Result<Response<Full<Bytes>>, std::convert::Infallible> {
    let method = req.method().as_str().to_string();
    let path = req.uri().path().to_string();

    // Build lowercase header map.
    let mut headers: HashMap<String, String> = HashMap::new();
    for (name, value) in req.headers() {
        if let Ok(v) = value.to_str() {
            headers.insert(name.as_str().to_lowercase(), v.to_string());
        }
    }

    // Check Content-Length upfront before collecting body.
    let content_length = headers
        .get("content-length")
        .and_then(|v| v.parse::<usize>().ok());

    if let Some(len) = content_length
        && len > MAX_BODY_BYTES
    {
        return Ok(status_response(StatusCode::PAYLOAD_TOO_LARGE));
    }

    // Collect body with a size cap.
    let limited = Limited::new(req.into_body(), MAX_BODY_BYTES);
    let bytes = match limited.collect().await {
        Ok(collected) => collected.to_bytes(),
        Err(_) => return Ok(status_response(StatusCode::PAYLOAD_TOO_LARGE)),
    };

    let webhook_req = WebhookRequest {
        method: &method,
        path: &path,
        headers,
        body: &bytes,
    };

    let status = handle_request(webhook_req, &secret);
    Ok(status_response(status))
}

fn status_response(status: StatusCode) -> Response<Full<Bytes>> {
    Response::builder()
        .status(status)
        .body(Full::new(Bytes::new()))
        .unwrap()
}

fn events_path_for_display() -> String {
    dirs::home_dir()
        .unwrap_or_else(std::env::temp_dir)
        .join(".local")
        .join("state")
        .join("git-orchard")
        .join("events.jsonl")
        .to_string_lossy()
        .to_string()
}
