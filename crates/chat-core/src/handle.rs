//! Handle derivation: tmux session name → user-facing handle.
//!
//! Slugify rules:
//! - Lowercase ASCII alphanum and underscores only.
//! - Non-alphanum runs collapse to a single underscore.
//! - Leading/trailing underscores stripped.
//! - Truncate to ≤24 chars on a word boundary (no trailing underscore).
//! - On collision with an already-taken set, suffix `_2`, `_3`, … until free.
//!
//! The handle is returned **without** the leading `@` — the `@` is a UI
//! convention (the JSONL `sender` field includes it; CLI args parse it).

/// Slugify a tmux session name to a chat handle.
///
/// `display_name` is currently unused but reserved for future UX (e.g. taking
/// the user's preferred display name when the tmux name is auto-generated).
pub fn derive_handle(tmux_session_name: &str, _display_name: Option<&str>) -> String {
    let mut out = String::with_capacity(24);
    let mut prev_was_underscore = false;

    for ch in tmux_session_name.chars() {
        let mapped = if ch.is_ascii_alphanumeric() {
            Some(ch.to_ascii_lowercase())
        } else {
            // Any non-alphanum becomes a separator. Collapse runs.
            None
        };
        match mapped {
            Some(c) => {
                out.push(c);
                prev_was_underscore = false;
            }
            None => {
                if !prev_was_underscore && !out.is_empty() {
                    out.push('_');
                    prev_was_underscore = true;
                }
            }
        }
    }
    // Strip trailing underscore if any.
    while out.ends_with('_') {
        out.pop();
    }
    if out.is_empty() {
        return "anon".to_string();
    }

    truncate_on_word_boundary(&out, 24)
}

/// Same as [`derive_handle`] but disambiguates against `taken`.
///
/// Returns `base` if free, else `base_2`, `base_3`, … until a free slot is
/// found. The suffix is appended *after* truncation, so the final string may
/// exceed 24 chars when many collisions stack — acceptable for a v1 fanout
/// surface where collisions are user-visible and rare.
pub fn derive_handle_with_collisions(
    tmux_session_name: &str,
    display_name: Option<&str>,
    taken: &[&str],
) -> String {
    let base = derive_handle(tmux_session_name, display_name);
    if !taken.contains(&base.as_str()) {
        return base;
    }
    let mut n: u32 = 2;
    loop {
        let candidate = format!("{base}_{n}");
        if !taken.contains(&candidate.as_str()) {
            return candidate;
        }
        n += 1;
        if n > 999 {
            // Pathological case: just return with the count.
            return candidate;
        }
    }
}

fn truncate_on_word_boundary(s: &str, max: usize) -> String {
    if s.len() <= max {
        return s.to_string();
    }
    // Cut at `max`, then walk back to the last `_` to avoid splitting a word
    // mid-character. If there's no `_` in the prefix, hard-cut.
    let mut cut = max;
    let bytes = s.as_bytes();
    while cut > 0 && bytes[cut - 1] != b'_' && cut == max {
        // Hard-cut path: just take the first `max` chars.
        return s[..max].trim_end_matches('_').to_string();
    }
    // Walk backward to last underscore for clean break (only if it's within
    // the last 8 chars, otherwise hard-cut yields a more legible name).
    let lookback_floor = max.saturating_sub(8);
    while cut > lookback_floor && bytes[cut - 1] != b'_' {
        cut -= 1;
    }
    if cut == lookback_floor {
        // No nearby underscore; hard-cut.
        return s[..max].trim_end_matches('_').to_string();
    }
    // cut points just past an underscore (or at the start). Trim the underscore.
    s[..cut].trim_end_matches('_').to_string()
}
