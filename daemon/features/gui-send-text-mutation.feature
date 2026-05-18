Feature: GUI send-text mutation — sendTextToPane
  As the orchard-gui SessionComposer
  I need to send user input to a live Claude REPL in a tmux pane
  So that the user can message Claude from the browser or mobile client without direct tmux access.

  Operations consumed:
    Mutation.sendTextToPane(paneId: String!, text: String!): Boolean!
    Tauri command: tmux_send_text(paneId, text)  [preferred path in desktop app]
    Fallback: HTTP POST to /__daemon/graphql with the mutation [browser/mobile]

  Background:
    Given the daemon is running on 127.0.0.1:7777
    And a live tmux pane "%26" is hosting a Claude REPL

  Scenario: Desktop path — Tauri bridge sends text without network hop
    Given the app is running in Tauri (window.__TAURI_INTERNALS__ is defined)
    When the user submits a message in the composer
    Then invoke("tmux_send_text", {paneId: "%26", text: "hello"}) is called
    And no HTTP request is made to the daemon GraphQL endpoint
    And the pending turn status advances to "sent" when the invoke resolves

  Scenario: Browser/mobile path — mutation proxied through daemon
    Given the app is running in a browser (no Tauri context)
    When the user submits a message in the composer
    Then a POST request is made to /__daemon/graphql (or http://127.0.0.1:7777/graphql)
    And the request body is {"query": "mutation($paneId: String!, $text: String!) { sendTextToPane(paneId: $paneId, text: $text) }", "variables": {"paneId": "%26", "text": "hello"}}
    And the daemon executes tmux send-keys for the target pane
    And the response body has data.sendTextToPane = true
    And the pending turn status advances to "sent"

  Scenario: Optimistic UI — input clears immediately before async ack
    When the user hits Enter in the composer
    Then the input textarea clears instantly (before the mutation resolves)
    And a "pending" turn bubble appears in the transcript with status "sending"
    And focus returns to the textarea without waiting for the mutation

  Scenario: Mutation returns error — pending turn is removed
    When sendTextToPane raises an error (tmux pane not reachable, daemon exits non-zero)
    Then the pending turn bubble is removed from the transcript
    And a toast error is shown with the error message
    And the textarea retains the failed text so the user can retry

  Scenario: Daemon validates paneId — non-existent pane returns GraphQL error
    Given pane "%999" does not exist
    When sendTextToPane is called with paneId = "%999"
    Then the daemon returns a GraphQL error with a descriptive message
    And the HTTP status is 200 (GraphQL errors are always HTTP 200)
    And the client surfaces the error as a toast

  Scenario: Mutation feedback loop — ConversationChanged fires after send
    Given the user sent a message and the pending turn is "sent"
    When the daemon's tmux send-keys executes successfully
    And Claude processes the message and appends a new assistant turn to the JSONL
    Then conversationChanged fires within 2 seconds of the initial send
    And the pending turn's status advances to "received" then "seen"
    And the iMessage-style indicator sequence completes: sending → sent → received → seen

  Scenario: Composer is hidden when no effectivePaneId
    Given OpenPanel resolved a session and conversation but tmuxPanes is empty
    Then SessionComposer is not rendered
    And the panel shows "No live tmux pane — open Terminal view to attach a fresh client."
