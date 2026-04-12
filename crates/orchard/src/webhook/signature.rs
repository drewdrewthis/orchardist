//! HMAC-SHA256 signature verification for GitHub webhooks.
//!
//! GitHub signs every webhook request with an HMAC-SHA256 digest computed over
//! the **raw** request body bytes and sends it in the `X-Hub-Signature-256`
//! header as `sha256=<hex>`. This module verifies that header in constant time.
//!
//! # Correctness note
//!
//! Verification MUST happen over the raw body bytes received from the network,
//! *before* any JSON parsing. If the body is deserialised and re-serialised the
//! signature will not match because JSON key order and whitespace may change.

use hmac::{Hmac, Mac};
use sha2::Sha256;

type HmacSha256 = Hmac<Sha256>;

const SHA256_PREFIX: &str = "sha256=";

/// Errors that can occur during signature verification.
#[derive(Debug, PartialEq, Eq)]
pub enum SignatureError {
    /// The header does not start with `"sha256="`.
    MissingPrefix,
    /// The hex-encoded digest in the header could not be decoded.
    InvalidHex,
    /// The computed HMAC does not match the header value.
    Mismatch,
}

impl std::fmt::Display for SignatureError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::MissingPrefix => write!(f, "signature header missing 'sha256=' prefix"),
            Self::InvalidHex => write!(f, "signature header contains invalid hex"),
            Self::Mismatch => write!(f, "signature mismatch"),
        }
    }
}

impl std::error::Error for SignatureError {}

/// Verify the GitHub `X-Hub-Signature-256` header against the raw request body.
///
/// `header` is the full header value (e.g. `"sha256=abc123..."`).
/// `body` is the **raw, unmodified** request body bytes.
/// `secret` is the webhook secret bytes used to initialise the HMAC key.
///
/// Uses a constant-time comparison via [`Mac::verify_slice`] so this function
/// is safe against timing side-channel attacks.
///
/// # Errors
///
/// Returns [`SignatureError::MissingPrefix`] when the header does not start
/// with `"sha256="`, [`SignatureError::InvalidHex`] when the hex portion
/// cannot be decoded, and [`SignatureError::Mismatch`] when the digest is
/// well-formed but does not match the computed HMAC.
pub fn verify_signature(header: &str, body: &[u8], secret: &[u8]) -> Result<(), SignatureError> {
    let hex_digest = header
        .strip_prefix(SHA256_PREFIX)
        .ok_or(SignatureError::MissingPrefix)?;

    let expected = hex::decode(hex_digest).map_err(|_| SignatureError::InvalidHex)?;

    let mut mac = HmacSha256::new_from_slice(secret).expect("HMAC accepts keys of any length");
    mac.update(body);
    mac.verify_slice(&expected)
        .map_err(|_| SignatureError::Mismatch)
}

#[cfg(test)]
mod tests {
    use super::*;
    use hmac::Mac;

    fn make_signature(body: &[u8], secret: &[u8]) -> String {
        let mut mac = HmacSha256::new_from_slice(secret).unwrap();
        mac.update(body);
        let result = mac.finalize().into_bytes();
        format!("sha256={}", hex::encode(result))
    }

    #[test]
    fn valid_signature_is_accepted() {
        let body = b"hello world";
        let secret = b"supersecret";
        let header = make_signature(body, secret);
        assert_eq!(verify_signature(&header, body, secret), Ok(()));
    }

    #[test]
    fn wrong_hex_digest_is_rejected() {
        let body = b"hello world";
        let secret = b"supersecret";
        assert_eq!(
            verify_signature("sha256=deadbeef", body, secret),
            Err(SignatureError::Mismatch)
        );
    }

    #[test]
    fn hmac_computed_with_wrong_secret_is_rejected() {
        let body = b"hello world";
        let header = make_signature(body, b"wrong");
        assert_eq!(
            verify_signature(&header, body, b"supersecret"),
            Err(SignatureError::Mismatch)
        );
    }

    #[test]
    fn header_missing_prefix_is_rejected() {
        let body = b"hello world";
        let secret = b"supersecret";
        // Compute a valid HMAC but strip the "sha256=" prefix.
        let mut mac = HmacSha256::new_from_slice(secret).unwrap();
        mac.update(body);
        let hex = hex::encode(mac.finalize().into_bytes());
        assert_eq!(
            verify_signature(&hex, body, secret),
            Err(SignatureError::MissingPrefix)
        );
    }

    #[test]
    fn empty_body_with_correct_hmac_is_accepted() {
        let body = b"";
        let secret = b"supersecret";
        let header = make_signature(body, secret);
        assert_eq!(verify_signature(&header, body, secret), Ok(()));
    }

    #[test]
    fn non_ascii_utf8_payload_verified_over_raw_bytes() {
        // The payload contains multi-byte UTF-8 (é = 0xC3 0xA9).
        // This tests that we hash the raw bytes, not a reparsed/reserialized form.
        let body = r#"{"actor":{"login":"café-bot"}}"#.as_bytes();
        let secret = b"supersecret";
        let header = make_signature(body, secret);
        assert_eq!(
            verify_signature(&header, body, secret),
            Ok(()),
            "HMAC over raw UTF-8 bytes must succeed"
        );
    }
}
