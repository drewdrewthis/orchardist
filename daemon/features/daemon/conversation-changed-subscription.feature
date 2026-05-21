@integration
Feature: Daemon conversationChanged subscription — push contract
  As any daemon consumer (GUI TranscriptView)
  I need the conversationChanged subscription to emit within 500ms when a JSONL is appended
  So that consumers can update transcripts without polling.

  Background:
    Given a daemon httptest.Server is running with fsnotify watching the claude projects directory
    And a WebSocket graphql-transport-ws connection is open to the daemon

  @integration
  Scenario: Subscription fires when JSONL file is appended
    Given a subscription to conversationChanged for sessionUuid "abc123" is open
    When a new record is appended to the matching JSONL on disk
    Then the daemon emits a conversationChanged event within 500ms of the file write
    And the payload contains sessionUuid = "abc123"
    And the payload contains an updated lastSeenAt timestamp
    And the payload contains an updated messageCount
    And no GraphQL errors are present

  @integration
  Scenario: conversationChanged payload — null on JSONL file removal
    Given a subscription to conversationChanged for sessionUuid "abc123" is open
    When the matching JSONL file is deleted from disk
    Then the daemon emits a null payload for the subscription
    And no daemon crash occurs

  @integration
  Scenario: Zero-sessionUuid guard — no subscription opened without a uuid
    When conversationChanged subscription is started without a sessionUuid
    Then the daemon returns a validation error
    And no subscription stream is opened
