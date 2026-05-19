Feature: GUI conversation subscription — ConversationChanged
  As the orchard-gui TranscriptView
  I need a push subscription that fires whenever the JSONL for my session is updated
  So that the transcript reloads without polling, and pending-turn states advance correctly.

  Operation consumed:
    subscription ConversationChanged($sessionUuid: String!) {
      conversationChanged(sessionUuid: $sessionUuid) {
        sessionUuid
        lastSeenAt
        messageCount
      }
    }

  Transport: graphql-ws WebSocket to ws://127.0.0.1:7777/graphql (or /__daemon/graphql via Vite proxy)

  Background:
    Given the daemon is running with fsnotify watching the claude projects directory
    And TranscriptView has mounted with a known sessionUuid
    And the WebSocket to the daemon is open

  @integration
  Scenario: Subscription fires when the JSONL file is appended
    Given TranscriptView has subscribed to conversationChanged for sessionUuid "abc123"
    When a new record is appended to the matching JSONL on disk
    Then the daemon emits a conversationChanged event within 500ms of the file write
    And the payload contains sessionUuid = "abc123"
    And the payload contains an updated lastSeenAt timestamp
    And the payload contains an updated messageCount

  @integration
  Scenario: TranscriptView debounces rapid pushes — no CPU storm on active turn
    Given Claude is actively writing a multi-block response (1 JSONL record per token batch)
    When conversationChanged fires at >5Hz
    Then TranscriptView coalesces the burst with a 350ms trailing debounce
    And the readTranscript call fires at most once per 350ms burst window
    And the browser remains responsive during a fast-writing turn

  @integration
  Scenario: Pending turn advances to "received" on subscription push
    Given the user sent a message and the pending turn is in "sent" state
    And the turns.length at send time was N
    When conversationChanged fires and readTranscript returns turns.length > N
    Then the pending turn's status advances to "received"

  @integration
  Scenario: Pending turn advances to "seen" when assistant turn appears
    Given a pending turn is in "received" state
    When conversationChanged fires and the fresh turns array contains a new assistant turn
    And the assistant turn's timestamp >= the pending turn's sentAt
    Then the pending turn's status advances to "seen"
    And the "seen" bubble fades out after 2 seconds

  @integration
  Scenario: Pending turn stalles after 90 seconds without advancing
    Given a pending turn has been in "sent" state for 90 seconds
    And no conversationChanged push has arrived during that window
    Then the pending turn's status flips to "stalled"
    And the bubble renders with "·waiting" indicator at reduced opacity

  @integration
  Scenario: Subscription error is logged but does not crash the view
    When the WebSocket connection drops mid-session
    Then TranscriptView logs a console.warn with "[transcript] subscription error:"
    And the transcript view continues to display the last-loaded turns
    And when the WebSocket reconnects, the subscription resumes from where it left off

  @integration
  Scenario: Subscription torn down when sessionUuid changes
    Given TranscriptView is subscribed to sessionUuid "abc123"
    When the panel selection changes to sessionUuid "def456"
    Then the "abc123" subscription is unsubscribed via the returned Unsub handle
    And a new subscription is opened for "def456"
    And no subscription leaks remain after the component unmounts

  @integration
  Scenario: Zero-sessionUuid guard — no subscription when uuid is absent
    When TranscriptView mounts without a sessionUuid prop
    Then no conversationChanged subscription is opened
    And the view loads from path alone when path is non-empty
