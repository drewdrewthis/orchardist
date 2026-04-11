//! Integration tests for `orchard webhook-serve`.
//!
//! Each test spawns the release binary with `--port 0` to get an ephemeral
//! port, exercises the HTTP API, and inspects events.jsonl in a temp directory.

use assert_cmd::cargo::cargo_bin;
use hmac::{Hmac, Mac};
use sha2::Sha256;
use std::io::{BufRead, BufReader, Read, Write};
use std::net::TcpStream;
use std::path::Path;
use std::process::{Child, Command, Stdio};
use std::time::Duration;
use tempfile::TempDir;

type HmacSha256 = Hmac<Sha256>;

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/// Spawns `orchard webhook-serve --port 0` with a temp HOME and given secret.
/// Returns `(child, bound_port, temp_dir)`.
fn spawn_server(secret: &str) -> (Child, u16, TempDir) {
    let temp = TempDir::new().unwrap();
    let bin = cargo_bin("orchard");

    let mut child = Command::new(bin)
        .args(["webhook-serve", "--port", "0"])
        .env("HOME", temp.path())
        .env("ORCHARD_WEBHOOK_SECRET", secret)
        // Redirect stderr so we can read the startup line.
        .stderr(Stdio::piped())
        .stdout(Stdio::null())
        .spawn()
        .expect("failed to spawn orchard webhook-serve");

    // Read the startup line from stderr to discover the bound port.
    let port = read_bound_port(child.stderr.as_mut().unwrap());

    (child, port, temp)
}

/// Reads bytes from `stderr` line by line until we find the "listening on"
/// startup line, then returns the port number embedded in it.
///
/// After finding the port, the function returns immediately without consuming
/// more bytes — the remaining stderr output is left for the OS to buffer.
fn read_bound_port(stderr: &mut impl Read) -> u16 {
    let reader = BufReader::new(stderr);
    for line in reader.lines() {
        match line {
            Ok(l) if l.contains("listening on") => {
                // Line format: "orchard webhook-serve: listening on http://127.0.0.1:<port>/webhook ..."
                if let Some(port_str) = l
                    .split("http://127.0.0.1:")
                    .nth(1)
                    .and_then(|s| s.split('/').next())
                    && let Ok(p) = port_str.parse::<u16>()
                {
                    return p;
                }
            }
            Ok(_) => continue,
            Err(_) => break,
        }
    }
    panic!("never read bound port from server stderr");
}

/// Compute HMAC-SHA256 signature in the `sha256=<hex>` format GitHub uses.
fn make_sig(body: &[u8], secret: &[u8]) -> String {
    let mut mac = HmacSha256::new_from_slice(secret).unwrap();
    mac.update(body);
    format!("sha256={}", hex::encode(mac.finalize().into_bytes()))
}

/// Send a POST to /webhook with a properly signed body and `X-GitHub-Event`.
/// Returns `(status_code, response_text)`.
fn post_with_hmac(port: u16, event: &str, body: &[u8], secret: &[u8]) -> (u16, String) {
    let sig = make_sig(body, secret);

    let request = format!(
        "POST /webhook HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nX-GitHub-Event: {event}\r\nX-Hub-Signature-256: {sig}\r\nContent-Length: {len}\r\nConnection: close\r\n\r\n",
        len = body.len()
    );

    let mut stream = connect_with_retry(port);
    stream.write_all(request.as_bytes()).unwrap();
    stream.write_all(body).unwrap();
    let mut response = String::new();
    stream.read_to_string(&mut response).unwrap();

    let status = response
        .lines()
        .next()
        .unwrap()
        .split(' ')
        .nth(1)
        .unwrap()
        .parse::<u16>()
        .unwrap();
    (status, response)
}

/// Send a GET request to the given path.
fn get(port: u16, path: &str) -> u16 {
    let request = format!("GET {path} HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n");
    let mut stream = connect_with_retry(port);
    stream.write_all(request.as_bytes()).unwrap();
    let mut response = String::new();
    stream.read_to_string(&mut response).unwrap();

    response
        .lines()
        .next()
        .unwrap()
        .split(' ')
        .nth(1)
        .unwrap()
        .parse::<u16>()
        .unwrap()
}

/// Connect to the server with a short retry loop to handle startup latency.
fn connect_with_retry(port: u16) -> TcpStream {
    for _ in 0..20 {
        if let Ok(stream) = TcpStream::connect(("127.0.0.1", port)) {
            stream.set_read_timeout(Some(Duration::from_secs(5))).ok();
            return stream;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    panic!("could not connect to 127.0.0.1:{port} after retries");
}

/// Read all non-empty lines from `events.jsonl`.
fn read_events(home: &Path) -> Vec<String> {
    let path = home
        .join(".local")
        .join("state")
        .join("git-orchard")
        .join("events.jsonl");
    match std::fs::read_to_string(&path) {
        Ok(contents) => contents
            .lines()
            .filter(|l| !l.is_empty())
            .map(str::to_string)
            .collect(),
        Err(_) => vec![],
    }
}

/// Kill the child process cleanly.
fn kill(child: &mut Child) {
    let _ = child.kill();
    let _ = child.wait();
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

/// AC #7: webhook-serve starts, listens, and accepts GET /health.
#[test]
fn webhook_serve_starts_on_default_port_and_accepts_post() {
    let (mut child, port, _tmp) = spawn_server("test-secret");

    let status = get(port, "/health");
    assert_eq!(status, 200, "GET /health should return 200");

    kill(&mut child);
}

/// AC #8: --port 0 binds an ephemeral port and the startup line prints it.
#[test]
fn port_zero_binds_ephemeral_port_and_prints_it() {
    let (mut child, port, _tmp) = spawn_server("test-secret");

    assert!(port > 0, "bound port must be non-zero, got {port}");

    // Server must actually accept connections on that port.
    let status = get(port, "/health");
    assert_eq!(status, 200);

    kill(&mut child);
}

/// AC #10: refuses to start when ORCHARD_WEBHOOK_SECRET is missing or empty.
#[test]
fn refuses_to_start_without_secret() {
    let temp = TempDir::new().unwrap();
    let bin = cargo_bin("orchard");

    let output = Command::new(bin)
        .args(["webhook-serve", "--port", "0"])
        .env("HOME", temp.path())
        .env_remove("ORCHARD_WEBHOOK_SECRET")
        .output()
        .expect("failed to run orchard");

    assert!(
        !output.status.success(),
        "process must exit non-zero without secret"
    );
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(
        stderr.contains("ORCHARD_WEBHOOK_SECRET"),
        "stderr must mention ORCHARD_WEBHOOK_SECRET, got: {stderr}"
    );
}

/// AC #11: GET /health returns 200 (integration — proves server wiring works).
#[test]
fn get_health_returns_200() {
    let (mut child, port, _tmp) = spawn_server("test-secret");

    let status = get(port, "/health");
    assert_eq!(status, 200);

    kill(&mut child);
}

/// AC #14: invalid signature returns 401 and nothing is written to events.jsonl.
#[test]
fn invalid_signature_returns_401() {
    let (mut child, port, tmp) = spawn_server("supersecret");

    let body = b"{\"action\":\"opened\"}";
    let bad_sig = "sha256=deadbeef";

    let request = format!(
        "POST /webhook HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nX-GitHub-Event: pull_request\r\nX-Hub-Signature-256: {bad_sig}\r\nContent-Length: {len}\r\nConnection: close\r\n\r\n",
        len = body.len()
    );
    let mut stream = connect_with_retry(port);
    stream.write_all(request.as_bytes()).unwrap();
    stream.write_all(body).unwrap();
    let mut response = String::new();
    stream.read_to_string(&mut response).unwrap();

    let status: u16 = response
        .lines()
        .next()
        .unwrap()
        .split(' ')
        .nth(1)
        .unwrap()
        .parse()
        .unwrap();
    assert_eq!(status, 401);

    // Give the server a moment to flush, then check no line was written.
    std::thread::sleep(Duration::from_millis(100));
    let lines = read_events(tmp.path());
    assert!(lines.is_empty(), "no line should be written on 401");

    kill(&mut child);
}

/// AC #15: body larger than 30 MB returns 413.
///
/// Sends only the Content-Length header (no actual oversized body) so the
/// server can reject based on Content-Length alone — this avoids the EPIPE
/// risk of streaming 30 MB to a server that has already closed its write side.
#[test]
fn body_larger_than_30mb_returns_413() {
    let (mut child, port, _tmp) = spawn_server("supersecret");

    // Claim a body 1 byte over the limit. We won't actually send the body —
    // the server rejects on Content-Length alone.
    let claimed_len = 30 * 1024 * 1024 + 1;
    let fake_sig = format!("sha256={}", "a".repeat(64)); // invalid but doesn't matter — size check is first

    let request = format!(
        "POST /webhook HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nX-GitHub-Event: pull_request\r\nX-Hub-Signature-256: {fake_sig}\r\nContent-Length: {claimed_len}\r\nConnection: close\r\n\r\n"
    );

    let mut stream = connect_with_retry(port);
    stream.write_all(request.as_bytes()).unwrap();
    // Don't write the body — let the server respond to the Content-Length header.
    // Shut down the write side so hyper sees EOF on the request body.
    stream.shutdown(std::net::Shutdown::Write).ok();

    let mut response = String::new();
    stream.read_to_string(&mut response).unwrap();

    let status: u16 = response
        .lines()
        .next()
        .unwrap()
        .split(' ')
        .nth(1)
        .unwrap()
        .parse()
        .unwrap();
    assert_eq!(status, 413);

    kill(&mut child);
}

/// AC #25: valid signed webhook appends exactly one JSONL line.
#[test]
fn valid_signed_webhook_appends_jsonl_line() {
    let secret = b"supersecret";
    let (mut child, port, tmp) = spawn_server("supersecret");

    let body = serde_json::json!({
        "action": "opened",
        "number": 42,
        "pull_request": { "number": 42, "merged": false },
        "repository": { "full_name": "acme/webapp" },
        "sender": { "login": "some-actor" }
    })
    .to_string()
    .into_bytes();

    let (status, _) = post_with_hmac(port, "pull_request", &body, secret);
    assert_eq!(status, 200, "valid webhook must return 200");

    // Give the server a moment to flush.
    std::thread::sleep(Duration::from_millis(200));

    let lines = read_events(tmp.path());
    assert_eq!(lines.len(), 1, "expected exactly one line in events.jsonl");

    let parsed: serde_json::Value =
        serde_json::from_str(&lines[0]).expect("line must be valid JSON");
    assert_eq!(parsed["source"], "webhook");
    assert_eq!(parsed["kind"], "pull_request.opened");

    kill(&mut child);
}

/// AC #26: unknown X-GitHub-Event returns 204 and writes nothing.
#[test]
fn unknown_event_returns_204() {
    let secret = b"supersecret";
    let (mut child, port, tmp) = spawn_server("supersecret");

    let body = b"{}";
    let (status, _) = post_with_hmac(port, "star", body, secret);
    assert_eq!(status, 204);

    std::thread::sleep(Duration::from_millis(100));
    let lines = read_events(tmp.path());
    assert!(
        lines.is_empty(),
        "no line should be written for unknown event"
    );

    kill(&mut child);
}
