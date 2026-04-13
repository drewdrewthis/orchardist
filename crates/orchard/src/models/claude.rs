//! Canonical Claude telemetry type, collapsing ClaudeSessionInfo / ClaudeEnrichment / JsonClaudeInfo.
use serde::{Deserialize, Serialize};

use crate::claude_state::ClaudeState;

/// Usage within a rolling rate-limit window.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RateLimit {
    /// Fraction of the window's token budget consumed (0.0–1.0+).
    pub used_pct: f64,
    /// ISO 8601 timestamp when this window resets.
    pub resets_at: Option<String>,
}

/// Rate-limit usage across all rolling windows for a Claude session.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RateLimits {
    /// Five-hour rolling token window usage.
    pub five_hour: Option<RateLimit>,
    /// Seven-day rolling token window usage.
    pub seven_day: Option<RateLimit>,
}

/// Canonical Claude session telemetry attached to a tmux session.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Claude {
    /// Structured activity state (working, idle, input, none).
    pub status: ClaudeState,
    /// Model identifier in use (e.g. `claude-opus-4-6`).
    pub model: Option<String>,
    /// Last tool invoked by Claude (cleared on Stop).
    pub last_tool: Option<String>,
    /// First line of the last user prompt, truncated to 80 chars.
    pub current_task: Option<String>,
    /// Unix epoch seconds when the session started.
    pub session_start_ts: Option<u64>,
    /// Total input tokens from the most recent assistant message.
    pub input_tokens: Option<u64>,
    /// Total output tokens from the most recent assistant message.
    pub output_tokens: Option<u64>,
    /// Cache creation input tokens from the most recent assistant message.
    pub cache_creation_input_tokens: Option<u64>,
    /// Cache read input tokens from the most recent assistant message.
    pub cache_read_input_tokens: Option<u64>,
    /// Fraction of the context window consumed (0.0–1.0).
    pub context_window_pct: Option<f64>,
    /// Estimated cost in USD for this session.
    pub cost_usd: Option<f64>,
    /// Total wall-clock duration of this session in milliseconds.
    pub total_duration_ms: Option<u64>,
    /// Rolling rate-limit usage for this session.
    pub rate_limits: Option<RateLimits>,
    /// Why Claude stopped on its last turn (e.g. `end_turn`, `max_tokens`).
    pub stop_reason: Option<String>,
    /// Number of conversation turns completed in this session.
    pub turn_count: Option<u32>,
    /// Age of the session in seconds, computed at serialization time. Not stored in cache.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub session_age_sec: Option<u64>,
    /// Unix epoch seconds when the state last transitioned.
    ///
    /// Only updated when the state value changes. Absent for hooks that don't
    /// yet write `state_changed_at`.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub state_changed_at: Option<u64>,
    /// Elapsed seconds since the current state was entered, computed at serialization time.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub state_elapsed_sec: Option<u64>,
}
