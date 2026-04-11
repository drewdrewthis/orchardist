//! Request handling pipeline: size cap → signature → normalize → append.
//!
//! The `handle_request` function takes the pieces extracted from a hyper
//! Request (method, path, headers, raw body bytes) and returns a response
//! status code. No hyper types leak in — this makes the handler trivially
//! unit-testable without spawning a server.

use crate::webhook::normalize::{NormalizeResult, normalize};
use crate::webhook::signature::verify_signature;
use http::StatusCode;
use std::collections::HashMap;

/// Maximum allowed request body size (30 MiB).
pub const MAX_BODY_BYTES: usize = 30 * 1024 * 1024;

/// The parts of an HTTP request needed by the webhook handler.
pub struct WebhookRequest<'a> {
    /// HTTP method, e.g. `"GET"` or `"POST"`.
    pub method: &'a str,
    /// Request path, e.g. `"/webhook"`.
    pub path: &'a str,
    /// Request headers with lowercased keys.
    pub headers: HashMap<String, String>,
    /// Raw request body bytes.
    pub body: &'a [u8],
}

/// Process a webhook request and return the appropriate HTTP status code.
///
/// Routes:
/// - `GET /health` → 200 (no signature required)
/// - `POST /webhook` → size cap → signature check → normalize → append
/// - Any other route/method → 404
pub fn handle_request(req: WebhookRequest<'_>, secret: &[u8]) -> StatusCode {
    handle_request_with_writer(req, secret, &mut |event| {
        crate::events::log_webhook_event(event);
    })
}

/// Lower-level version that accepts an injectable writer for testability.
///
/// The `writer` closure is called with a `NormalizedEvent` when a valid,
/// recognised webhook arrives. The top-level `handle_request` wires this
/// to `events::log_webhook_event`.
pub fn handle_request_with_writer<F>(
    req: WebhookRequest<'_>,
    secret: &[u8],
    writer: &mut F,
) -> StatusCode
where
    F: FnMut(&crate::webhook::normalize::NormalizedEvent),
{
    match (req.method, req.path) {
        ("GET", "/health") => StatusCode::OK,
        ("POST", "/webhook") => handle_post_webhook(req.headers, req.body, secret, writer),
        _ => StatusCode::NOT_FOUND,
    }
}

fn handle_post_webhook<F>(
    headers: HashMap<String, String>,
    body: &[u8],
    secret: &[u8],
    writer: &mut F,
) -> StatusCode
where
    F: FnMut(&crate::webhook::normalize::NormalizedEvent),
{
    // 1. Size cap — fail fast before signature or JSON parsing.
    if body.len() > MAX_BODY_BYTES {
        return StatusCode::PAYLOAD_TOO_LARGE;
    }

    // 2. Signature verification over raw bytes.
    let sig_header = match headers.get("x-hub-signature-256") {
        Some(v) => v.as_str(),
        None => return StatusCode::UNAUTHORIZED,
    };
    if verify_signature(sig_header, body, secret).is_err() {
        return StatusCode::UNAUTHORIZED;
    }

    // 3. Parse JSON (after signature — do NOT reparse for sig).
    let payload: serde_json::Value = match serde_json::from_slice(body) {
        Ok(v) => v,
        Err(_) => return StatusCode::BAD_REQUEST,
    };

    // 4. Read event type header.
    let event_type = match headers.get("x-github-event") {
        Some(v) => v.as_str(),
        None => return StatusCode::BAD_REQUEST,
    };

    // 5. Normalize and dispatch.
    match normalize(event_type, &payload) {
        NormalizeResult::Event(e) => {
            writer(&e);
            StatusCode::OK
        }
        NormalizeResult::Unsupported | NormalizeResult::Unknown => StatusCode::NO_CONTENT,
    }
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::webhook::normalize::NormalizedEvent;
    use hmac::{Hmac, Mac};
    use sha2::Sha256;

    type HmacSha256 = Hmac<Sha256>;

    fn make_sig(body: &[u8], secret: &[u8]) -> String {
        let mut mac = HmacSha256::new_from_slice(secret).unwrap();
        mac.update(body);
        format!("sha256={}", hex::encode(mac.finalize().into_bytes()))
    }

    fn headers_with_sig(body: &[u8], secret: &[u8], event: &str) -> HashMap<String, String> {
        let mut h = HashMap::new();
        h.insert(
            "x-hub-signature-256".to_string(),
            make_sig(body, secret),
        );
        h.insert("x-github-event".to_string(), event.to_string());
        h
    }

    fn pr_opened_body() -> Vec<u8> {
        serde_json::json!({
            "action": "opened",
            "number": 42,
            "pull_request": { "number": 42, "merged": false },
            "repository": { "full_name": "acme/webapp" },
            "sender": { "login": "some-actor" }
        })
        .to_string()
        .into_bytes()
    }

    #[test]
    fn get_health_returns_200() {
        let req = WebhookRequest {
            method: "GET",
            path: "/health",
            headers: HashMap::new(),
            body: b"",
        };
        assert_eq!(handle_request(req, b"secret"), StatusCode::OK);
    }

    #[test]
    fn post_webhook_with_valid_signature_and_pr_opened_returns_200() {
        let secret = b"test-secret";
        let body = pr_opened_body();
        let headers = headers_with_sig(&body, secret, "pull_request");

        let mut written: Vec<NormalizedEvent> = Vec::new();
        let req = WebhookRequest {
            method: "POST",
            path: "/webhook",
            headers,
            body: &body,
        };
        let status = handle_request_with_writer(req, secret, &mut |e| written.push(e.clone()));

        assert_eq!(status, StatusCode::OK);
        assert_eq!(written.len(), 1);
        assert_eq!(written[0].kind, "pull_request.opened");
    }

    #[test]
    fn post_webhook_with_invalid_signature_returns_401() {
        let body = pr_opened_body();
        let mut headers = HashMap::new();
        headers.insert(
            "x-hub-signature-256".to_string(),
            "sha256=deadbeef".to_string(),
        );
        headers.insert("x-github-event".to_string(), "pull_request".to_string());

        let req = WebhookRequest {
            method: "POST",
            path: "/webhook",
            headers,
            body: &body,
        };
        assert_eq!(handle_request(req, b"secret"), StatusCode::UNAUTHORIZED);
    }

    #[test]
    fn post_webhook_with_missing_signature_returns_401() {
        let body = pr_opened_body();
        let mut headers = HashMap::new();
        headers.insert("x-github-event".to_string(), "pull_request".to_string());

        let req = WebhookRequest {
            method: "POST",
            path: "/webhook",
            headers,
            body: &body,
        };
        assert_eq!(handle_request(req, b"secret"), StatusCode::UNAUTHORIZED);
    }

    #[test]
    fn post_webhook_with_body_larger_than_max_returns_413() {
        let secret = b"secret";
        let body = vec![0u8; MAX_BODY_BYTES + 1];
        // Even with a valid signature the size check fires first.
        let headers = headers_with_sig(&body, secret, "pull_request");

        let req = WebhookRequest {
            method: "POST",
            path: "/webhook",
            headers,
            body: &body,
        };
        assert_eq!(handle_request(req, secret), StatusCode::PAYLOAD_TOO_LARGE);
    }

    #[test]
    fn post_webhook_with_unknown_event_returns_204() {
        let secret = b"secret";
        let body = b"{}";
        let headers = headers_with_sig(body, secret, "star");

        let req = WebhookRequest {
            method: "POST",
            path: "/webhook",
            headers,
            body,
        };
        assert_eq!(handle_request(req, secret), StatusCode::NO_CONTENT);
    }

    #[test]
    fn post_webhook_with_ping_event_returns_204() {
        let secret = b"secret";
        let body = b"{}";
        let headers = headers_with_sig(body, secret, "ping");

        let req = WebhookRequest {
            method: "POST",
            path: "/webhook",
            headers,
            body,
        };
        assert_eq!(handle_request(req, secret), StatusCode::NO_CONTENT);
    }

    #[test]
    fn post_webhook_with_unsupported_action_returns_204() {
        let secret = b"secret";
        let body = serde_json::json!({
            "action": "assigned",
            "pull_request": { "number": 1 },
            "repository": { "full_name": "acme/webapp" },
            "sender": { "login": "actor" }
        })
        .to_string()
        .into_bytes();
        let headers = headers_with_sig(&body, secret, "pull_request");

        let req = WebhookRequest {
            method: "POST",
            path: "/webhook",
            headers,
            body: &body,
        };
        assert_eq!(handle_request(req, secret), StatusCode::NO_CONTENT);
    }

    #[test]
    fn wrong_route_returns_404() {
        let req = WebhookRequest {
            method: "GET",
            path: "/webhook",
            headers: HashMap::new(),
            body: b"",
        };
        assert_eq!(handle_request(req, b"secret"), StatusCode::NOT_FOUND);

        let req2 = WebhookRequest {
            method: "POST",
            path: "/other",
            headers: HashMap::new(),
            body: b"",
        };
        assert_eq!(handle_request(req2, b"secret"), StatusCode::NOT_FOUND);
    }
}
