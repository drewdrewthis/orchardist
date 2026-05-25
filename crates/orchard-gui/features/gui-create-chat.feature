Feature: GUI create + chat from the browser — launchSession + sendTextToPane
  As a user on the live HTTPS GUI (no Tauri desktop app)
  I want to create a Claude session and chat with it entirely from the browser
  So that I can drive a fresh REPL and see its replies without terminal access.

  Operations consumed:
    Mutation.launchSession(input: LaunchSessionInput!): LaunchSessionResult!
    Mutation.sendTextToPane(paneId: String!, text: String!): Boolean!
    Subscription.conversationChanged(sessionUuid: String!): Conversation
    GET /v1/conversations/<sessionUuid>/jsonl   (transcript body)

  These scenarios are driven end-to-end against the LIVE HTTPS endpoint by the
  companion driver `tests/create-chat.drive.mjs` (a plain Playwright script — no
  Gherkin runner). Every Then/And line below maps 1:1 to one logged assertion in
  that driver, so a green driver run proves this file scenario-for-scenario.

  Background:
    Given the daemon is running and reachable through the GUI's /__daemon proxy
    And the GUI is loaded in a desktop-width browser (surface = desktop, not Tauri)

  @live
  Scenario: Create a real Claude session from the New Conversation modal
    When the user clicks New and the Launch button in the modal
    Then a success toast "Launched <sessionName>" is shown
    And a new tmux session appears that was not present before the click
    And that session's pane is running the claude command

  @live
  Scenario: A browser-created session opens a usable chat composer
    When the launched session opens in the deep view
    Then the message composer textarea (placeholder "Message…") is visible
    And no "No tmux pane resolved" or "desktop app required" placeholder is shown
    # The composer renders on the raw paneId before the daemon indexes the pane,
    # because browser desktop now defaults to chat view (inTauri() gate).

  @live
  Scenario: Sending a message submits it to the pane (sendTextToPane)
    When the user types a message and presses Enter in the composer
    Then the textarea clears instantly (optimistic, before the mutation resolves)
    And the message text reaches the target tmux pane and is submitted to Claude
    # Submit needs the two-step send-keys + gap fix; a fresh REPL otherwise leaves
    # the text unsent at the prompt (Enter absorbed by paste-coalesce).

  @live
  Scenario: Claude's reply streams into the transcript without a manual refresh
    Given the user has sent a message to the session
    When Claude appends its reply to the JSONL on disk
    Then conversationChanged fires over the WebSocket and the transcript re-fetches
    And an assistant turn ([data-role="assistant"]) containing the reply renders in the GUI
    # Live WS requires the daemon started with ORCHARD_ALLOWED_ORIGINS = the boxd
    # HTTPS origin, else the subscription handshake is refused (403).

  @live
  Scenario: End-to-end create → chat → reply entirely on the live HTTPS URL
    When the user creates a session, sends a message, and waits for the reply
    Then the whole flow completes from https://orchard-gui.drewdrewthis.boxd.sh
    And there are no uncaught page errors during the flow
