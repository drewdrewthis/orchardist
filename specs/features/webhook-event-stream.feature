Feature: GitHub webhook receiver and event stream
  As an orchard operator running the watch daemon
  I want orchard to receive GitHub webhooks and append them to the event log
  So that the watch daemon reacts to GitHub activity within 1-2 seconds instead of waiting on the 60s poll cycle

  Background:
    Given the event log path is "~/.local/state/git-orchard/events.jsonl"
    And the default webhook port is 8477
    And the webhook secret is read only from the environment variable "ORCHARD_WEBHOOK_SECRET" (no flag, no config file)
    And "orchard webhook-serve" and "orchard watch" are separate processes that share only the local events.jsonl file
    And the design is local-only: both processes must run on the same host because events.jsonl is not synchronized

  # ===================================================================
  # Subcommand wiring — orchard webhook-serve
  # ===================================================================

  @integration
  Scenario: webhook-serve starts and listens on the default port
    Given ORCHARD_WEBHOOK_SECRET is set to "test-secret"
    When the user runs "orchard webhook-serve"
    Then an HTTP server listens on port 8477
    And the server accepts POST requests at "/webhook"
    And a startup message prints the bound port and events.jsonl path to stderr

  @integration
  Scenario: --port 0 binds an ephemeral port and prints the chosen port
    Given ORCHARD_WEBHOOK_SECRET is set to "test-secret"
    When the user runs "orchard webhook-serve --port 0"
    Then the startup message prints a non-zero port discovered at runtime
    And the server accepts POST requests on that port

  @unit
  Scenario Outline: port resolution precedence is flag > env > config > default
    Given the candidate inputs <flag>, <env>, <config>
    When port resolution runs
    Then the resolved port is <resolved>

    Examples:
      | flag | env  | config | resolved |
      | 9000 | 9001 | 9002   | 9000     |
      | none | 9001 | 9002   | 9001     |
      | none | none | 9002   | 9002     |
      | none | none | none   | 8477     |

  @integration
  Scenario: webhook-serve refuses to start when ORCHARD_WEBHOOK_SECRET is missing or empty
    Given ORCHARD_WEBHOOK_SECRET is unset or empty
    When the user runs "orchard webhook-serve"
    Then the process exits with a non-zero status
    And stderr contains a message directing the user to set ORCHARD_WEBHOOK_SECRET

  @integration
  Scenario: GET /health returns 200 for a trivial liveness probe
    Given ORCHARD_WEBHOOK_SECRET is set to "test-secret"
    And the webhook server is running
    When a GET request arrives at "/health"
    Then the response status is 200
    And the health response does not require a signature

  # ===================================================================
  # HMAC-SHA256 signature validation
  #
  # The server verifies HMAC over the RAW request bytes before any JSON
  # parsing. This is the single most failure-prone part of GitHub webhook
  # receivers and these scenarios guard the byte-identical path.
  # ===================================================================

  @unit
  Scenario Outline: HMAC verification accepts and rejects the expected cases
    Given the webhook secret is "supersecret"
    And the raw body bytes are <body>
    And the signature header is <header>
    When verification runs
    Then the result is <result>

    Examples:
      | body                       | header                      | result |
      | "hello world"              | correct sha256=<hmac>       | accept |
      | "hello world"              | sha256=deadbeef             | reject |
      | "hello world"              | hmac computed with "wrong"  | reject |
      | "hello world"              | header missing sha256= prefix | reject |
      | ""                         | correct sha256=<hmac of ""> | accept |

  @unit
  Scenario: HMAC verification succeeds for a payload containing non-ASCII bytes
    Given the webhook secret is "supersecret"
    And a payload whose actor login is "café-bot" (multi-byte UTF-8)
    And the signature header was computed over the exact raw bytes of the payload
    When verification runs against the raw bytes
    Then verification succeeds
    # Why this matters: this is the canonical regression for "HMAC computed
    # over reparsed JSON" bugs. Any implementation that deserializes the body
    # before verifying will fail this scenario because reserialization drops
    # key ordering and whitespace.

  @integration
  Scenario: Invalid or missing signature returns HTTP 401 and nothing is written
    Given ORCHARD_WEBHOOK_SECRET is set to "supersecret"
    And the webhook server is running
    When a POST to "/webhook" arrives with an invalid or missing X-Hub-Signature-256
    Then the response status is 401
    And no line is appended to events.jsonl

  # ===================================================================
  # Body size cap — DoS protection
  # ===================================================================

  @integration
  Scenario: Request bodies larger than 30 MB are rejected with 413
    Given ORCHARD_WEBHOOK_SECRET is set to "supersecret"
    And the webhook server is running
    When a POST to "/webhook" arrives with a body larger than 30 MB
    Then the response status is 413
    And the server does not allocate the full body into memory before rejecting
    And no line is appended to events.jsonl

  # ===================================================================
  # Event normalization
  # ===================================================================

  @unit
  Scenario: Normalized line contains source, kind, repo, pr, actor, ts, data
    Given a valid pull_request payload with action "opened" for repo "acme/webapp" PR 42 by user "octocat"
    When the payload is normalized
    Then the resulting JSON line contains:
      | field  | value                 |
      | source | "webhook"             |
      | kind   | "pull_request.opened" |
      | repo   | "acme/webapp"         |
      | pr     | 42                    |
      | actor  | "octocat"             |
    And "ts" is a valid ISO 8601 UTC timestamp
    And "data" is the full raw GitHub payload passed through verbatim

  @unit
  Scenario Outline: pull_request actions map to pull_request.<action> kinds
    Given a valid pull_request payload with action "<action>" and merged <merged>
    When the payload is normalized
    Then the "kind" field is "<expected_kind>"

    # Note: action "opened" is covered by the full-fields scenario above;
    # this outline only enumerates the remaining actions.
    Examples:
      | action              | merged | expected_kind                    |
      | closed              | false  | pull_request.closed              |
      | closed              | true   | pull_request.merged              |
      | reopened            | false  | pull_request.reopened            |
      | ready_for_review    | false  | pull_request.ready_for_review    |
      | converted_to_draft  | false  | pull_request.converted_to_draft  |

  @unit
  Scenario: pull_request_review submitted maps to pull_request.review.submitted
    Given a valid pull_request_review payload with action "submitted" for PR 99
    When the payload is normalized
    Then "kind" is "pull_request.review.submitted"
    And "pr" is 99

  @unit
  Scenario: pull_request_review_comment created maps to pull_request.review_comment.created
    Given a valid pull_request_review_comment payload for PR 3099 by "coderabbitai[bot]"
    When the payload is normalized
    Then "kind" is "pull_request.review_comment.created"
    And "pr" is 3099
    And "actor" is "coderabbitai[bot]"

  @unit
  Scenario Outline: issue_comment populates pr when the issue is a PR
    Given an issue_comment payload with action "created" on issue <number> where is_pr is <is_pr>
    When the payload is normalized
    Then "kind" is "issue_comment.created"
    And "issue" is <number>
    And "pr" is <pr_field>

    Examples:
      | number | is_pr | pr_field |
      | 42     | true  | 42       |
      | 77     | false | absent   |

  @unit
  Scenario Outline: issues actions map to issues.<action> kinds
    Given an issues payload with action "<action>" on issue 77
    When the payload is normalized
    Then "kind" is "issues.<action>"
    And "issue" is 77

    Examples:
      | action    |
      | opened    |
      | closed    |
      | labeled   |
      | unlabeled |

  @unit
  Scenario: push event carries ref and commit count
    Given a push payload on ref "refs/heads/main" for "acme/webapp" with 3 commits
    When the payload is normalized
    Then "kind" is "push"
    And "repo" is "acme/webapp"
    And "data.ref" is "refs/heads/main"
    And "data.commits" has length 3

  @unit
  Scenario Outline: check_run, check_suite, and workflow_run completions carry the conclusion
    Given a <event> payload with action "completed" and conclusion "<conclusion>"
    When the payload is normalized
    Then "kind" is "<event>.completed"
    And "data.conclusion" is "<conclusion>"

    Examples:
      | event        | conclusion |
      | check_run    | failure    |
      | check_suite  | success    |
      | workflow_run | success    |

  @unit
  Scenario Outline: Unsupported actions on a known event type are normalized as unsupported
    Given a <event> payload with action "<action>"
    When the payload is normalized
    Then normalization returns "unsupported"
    And no line is written to events.jsonl

    Examples:
      | event        | action     |
      | pull_request | assigned   |
      | check_run    | created    |
      | check_suite  | requested  |
      | workflow_run | requested  |

  @integration
  Scenario: A valid signed webhook appends exactly one line to events.jsonl
    Given ORCHARD_WEBHOOK_SECRET is set to "supersecret"
    And the webhook server is running
    When a POST to "/webhook" arrives with a valid signature and X-GitHub-Event "pull_request"
    And the JSON body is a pull_request payload with action "opened" for "acme/webapp" PR 42
    Then the response status is 2xx
    And exactly one new line is appended to events.jsonl
    And the line parses as JSON containing source "webhook" and kind "pull_request.opened"

  # ===================================================================
  # Unknown and unsupported events
  # ===================================================================

  @integration
  Scenario Outline: Unknown or ignored X-GitHub-Event types return 204 and write nothing
    Given ORCHARD_WEBHOOK_SECRET is set to "supersecret"
    And the webhook server is running
    When a POST with a valid signature and X-GitHub-Event "<event>" arrives
    Then the response status is 204
    And no line is appended to events.jsonl
    And no error is logged

    Examples:
      | event |
      | ping  |
      | star  |
      | fork  |

  # ===================================================================
  # Co-existence with existing events.jsonl writers (task/session events)
  # ===================================================================

  @integration
  Scenario: Webhook line uses source/kind while task line uses event, both in the same events.jsonl
    Given an empty events.jsonl
    When a webhook event and a task.created event are appended in sequence
    Then the webhook line has "source" equal to "webhook" and a "kind" field
    And the task line has "event" equal to "task.created" and no "source" field
    And both lines parse as valid JSON objects on separate lines

  @integration
  Scenario: Interleaved writes from webhook-serve and task logger produce one line per write
    Given ORCHARD_WEBHOOK_SECRET is set to "supersecret"
    And the webhook server is running
    When 20 webhook POSTs and 20 task.created log calls are interleaved
    Then events.jsonl contains exactly 40 additional lines
    And every line parses as valid JSON on a single line
    And no line is truncated or merged with another

  @integration
  Scenario: Webhook appends trigger the existing 50 MB rotation
    Given events.jsonl is already 50 MB or larger
    And ORCHARD_WEBHOOK_SECRET is set to "supersecret"
    And the webhook server is running
    When a valid signed webhook is received
    Then events.jsonl is rotated to events.jsonl.1
    And the new events.jsonl contains the freshly appended webhook line

  # ===================================================================
  # Watch daemon — events.jsonl tailer integration
  #
  # The tailer is inside the existing sync daemon. It must be correct
  # under these adversarial conditions: sub-second writes, rotation,
  # partial lines, daemon restart, and file missing.
  # ===================================================================

  @unit
  Scenario: Tailer advances offset only past the last complete newline
    Given events.jsonl contains "line1\nline2\npartial" with no trailing newline
    When the tailer reads from offset 0
    Then "line1" and "line2" are emitted
    And the stored offset is at the byte just after the second "\n"
    And "partial" is NOT emitted
    And on the next read with "partial" now completed by "\n", "partial" is emitted

  @unit
  Scenario: Tailer triggers a read whenever the file size exceeds the stored offset, regardless of mtime
    Given the tailer stored offset 500 and mtime T
    And events.jsonl is now 800 bytes with mtime still T
    When the tailer runs
    Then bytes 500..800 are read
    And the stored offset is 800
    # Reason: mtime resolution is 1 second on macOS/NFS; a write within
    # the same second would be missed if mtime-equality were a short-circuit.

  @unit
  Scenario: Tailer resets to offset 0 when the file is shorter than the stored offset
    Given the tailer stored offset 1000
    When the tailer sees events.jsonl at 200 bytes
    Then the stored offset is reset to 0 and a read starts from 0

  @unit
  Scenario: Tailer ignores non-webhook lines and malformed JSON without losing progress
    Given events.jsonl contains a task.created line, a malformed JSON line, and a webhook line
    When the tailer reads all three
    Then only the webhook line is forwarded to the refresh trigger
    And the task and malformed lines are skipped silently
    And the offset advances past all three

  @unit
  Scenario: Tailer starts from end-of-file on a cold start
    Given the watch daemon is starting
    And events.jsonl already contains 100 historical lines
    When the tailer initializes
    Then the stored offset is set to the current file size
    And the 100 historical lines are NOT replayed
    # Reason: events.jsonl is an append-only log of realtime activity;
    # a daemon starting fresh wants the current state of the world,
    # not a replay of history.

  @integration
  Scenario: A webhook line triggers a daemon refresh within 2 seconds
    Given the watch daemon is running with local_poll_secs 10 and full_poll_secs 60
    And the tailer is active
    When a webhook-source line is appended to events.jsonl
    Then within 2 seconds the daemon performs a refresh
    And the refresh happens regardless of the poll interval

  @integration
  Scenario: Multiple webhook lines between iterations debounce to one refresh
    Given the watch daemon is running
    When 5 webhook lines are appended to events.jsonl between two loop iterations
    Then the daemon performs exactly one refresh for the batch
    And the offset advances past all 5 lines

  @integration
  Scenario: Watch daemon falls back to poll-only when events.jsonl is missing or unreadable
    Given the watch daemon is running
    And events.jsonl does not exist OR exists but is unreadable
    When the daemon loop iterates repeatedly
    Then the daemon continues polling on its configured intervals
    And the daemon does not crash
    And if the file is unreadable, a single warning is logged; if it has never existed the daemon stays silent

  # ===================================================================
  # End-to-end — hermetic integration test
  # ===================================================================

  @e2e
  Scenario: Hermetic test spawns webhook-serve, POSTs a signed payload, sees JSONL append
    Given ORCHARD_WEBHOOK_SECRET is set in the test environment
    When the test spawns "orchard webhook-serve --port 0"
    And the test discovers the bound port from the server's startup output
    And the test POSTs a signed pull_request "opened" payload
    Then the response is 2xx
    And a new line appears in the test events.jsonl within 1 second
    And the line parses as JSON with source "webhook" and kind "pull_request.opened"

  @e2e
  Scenario: Full loop — webhook-serve + watch daemon + tailer + subscriber
    Given ORCHARD_WEBHOOK_SECRET is set
    And "orchard webhook-serve" is running on a random port
    And "orchard watch" is running in event-tailing mode
    And a tmux subscriber is registered
    When GitHub POSTs a signed pull_request_review_comment webhook
    Then the webhook is written to events.jsonl
    And the watch daemon triggers a refresh within 2 seconds
    And the subscriber receives a watch event

  # ===================================================================
  # Documentation
  # ===================================================================

  @unit
  Scenario: docs/webhook-setup.md documents dev and prod setup
    Then the file "docs/webhook-setup.md" exists
    And it describes configuring a GitHub webhook (payload URL, content type, secret, events)
    And it describes local dev setup via smee.io
    And it describes production setup via a public endpoint behind a TLS reverse proxy
    And it states the server speaks plain HTTP only (TLS is the operator's responsibility)
    And it explains how to set ORCHARD_WEBHOOK_SECRET
    And it documents that webhook-serve and watch must run on the same host (local events.jsonl)
    And it includes a troubleshooting section covering signature mismatches, smee disconnects, and laptop sleep

  @unit
  Scenario: docs/architecture.md describes the hybrid poll + webhook watch model
    Then "docs/architecture.md" contains a section describing the event-driven watch hybrid
    And it explains webhook-serve writes normalized JSONL to events.jsonl
    And it explains the watch daemon tails events.jsonl to short-circuit the 60s poll cycle
    And it documents the rationale for file-as-queue over unix-socket IPC (persistence across daemon restarts, multi-consumer tailing, simpler operational model)
    And it documents the two-shape coexistence rule: consumers distinguish webhook lines by presence of "source" = "webhook"
