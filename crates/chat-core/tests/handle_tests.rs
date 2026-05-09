//! Unit tests for `derive_handle` slugify + collision suffixing.

use chat_core::{derive_handle, derive_handle_with_collisions};

#[test]
fn lowercases_and_drops_non_alphanum() {
    assert_eq!(derive_handle("Alice", None), "alice");
    assert_eq!(derive_handle("Issue-123/Foo", None), "issue_123_foo");
    assert_eq!(derive_handle("ALPHA-BETA", None), "alpha_beta");
}

#[test]
fn collapses_runs_of_separators() {
    assert_eq!(derive_handle("foo---bar", None), "foo_bar");
    assert_eq!(derive_handle("foo  bar", None), "foo_bar");
    assert_eq!(derive_handle("foo/-/bar", None), "foo_bar");
}

#[test]
fn strips_trailing_underscores() {
    assert_eq!(derive_handle("alice-", None), "alice");
    assert_eq!(derive_handle("alice---", None), "alice");
    assert_eq!(derive_handle("alice_", None), "alice");
}

#[test]
fn truncates_to_24_chars() {
    let long = "a".repeat(50);
    let out = derive_handle(&long, None);
    assert!(out.len() <= 24, "got {} chars: {out:?}", out.len());
}

#[test]
fn truncates_on_word_boundary_when_close() {
    // "issue_123_long_branch_name" is 26 chars; truncate near a `_` boundary.
    let out = derive_handle("issue-123-long-branch-name", None);
    assert!(out.len() <= 24);
    assert!(!out.ends_with('_'), "no trailing underscore: {out}");
}

#[test]
fn empty_input_yields_anon() {
    assert_eq!(derive_handle("", None), "anon");
    assert_eq!(derive_handle("---", None), "anon");
}

#[test]
fn collision_suffixes_with_2_3_etc() {
    let taken: Vec<&str> = vec!["alice"];
    let out = derive_handle_with_collisions("Alice", None, &taken);
    assert_eq!(out, "alice_2");

    let taken: Vec<&str> = vec!["alice", "alice_2", "alice_3"];
    let out = derive_handle_with_collisions("Alice", None, &taken);
    assert_eq!(out, "alice_4");
}

#[test]
fn no_collision_returns_base() {
    let taken: Vec<&str> = vec!["bob", "carol"];
    let out = derive_handle_with_collisions("Alice", None, &taken);
    assert_eq!(out, "alice");
}
