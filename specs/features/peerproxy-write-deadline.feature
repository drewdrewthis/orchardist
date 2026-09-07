Feature: peerproxy websocket write deadline (#759)
  As the federation peerproxy transport
  I want every websocket send to carry a write deadline that feeds the reconnect path
  So that a half-open or stalled peer (TCP receive window full, never draining) cannot park a write forever, deadlock the write path, or strand the peer on a dead connection

  Background:
    Given a peerproxy Client connected to a fake graphql-transport-ws peer via the hermetic "fakePeerWS" harness
    And the harness reports an accepted-upgrade counter so a redial is observable
    And "writeJSON" serialises every outbound frame under "writeMu"
    And the read-side fix (#732 / PR #755) arms a read deadline from "readWait"
    And to isolate the write deadline the client is built with "readWait" left large (>= 5s, above the 2s test guard) and only "writeWait" shrunk to ~150ms
    And on origin/main "writeJSON" arms no write deadline and a write error is not fed into "failAll"

  # AC1
  @integration
  Scenario: Write to a non-reading peer returns a timeout instead of parking
    Given a peer that completes the connection_init / connection_ack / subscribe handshake then stops reading from its socket so the client send buffer fills
    When the client issues a send through "writeJSON"
    Then the surfaced error satisfies errors.As(err, &netErr) with netErr.Timeout() == true
    And the error is returned within "writeWait" rather than parking past the 2s guard
    And the trip originates from the write path, not a "ws read:" error

  # AC2
  @integration
  Scenario: Pong-reply write timeout errors the stream and redials
    Given an established subscription on a peer that sends a "ping" data frame then stops reading
    When the readLoop's "pong" reply write times out
    Then the connection is torn down via "failAll"
    And the subscription channel receives one final QueryResult with a non-empty Errors slice and then closes within 2s
    And a subsequent Subscribe causes a new websocket upgrade so the accepted-upgrade counter goes from 1 to at least 2

  # AC3
  @integration
  Scenario: Subscribe-frame write timeout resets the connection instead of stranding it
    Given a peer that completes the handshake then stops reading so the subscribe-frame write cannot drain
    When two consecutive Subscribe attempts are made against the same client
    Then the first attempt's write times out and the cached connection is reset
    And the second attempt triggers a redial so the accepted-upgrade counter increases
    And the client does not reuse the first dead connection

  # AC4
  @integration
  Scenario: Healthy traffic with sub-deadline gaps is never falsely timed out
    Given a peer that reads normally and delivers "next" frames with inter-frame gaps each shorter than "writeWait"
    And the total span of delivery exceeds "writeWait"
    When the subscription runs to completion
    Then every frame is delivered with no error frame
    And a deliberate mutation that arms the deadline once cumulatively instead of fresh per "writeJSON" call makes this scenario go red

  # AC5
  @unit
  Scenario: Write-deadline policy mirrors the read-deadline shape
    Given the peerproxy package defines "defaultReadWait" and a "readWait" field defaulted from it
    When the write-deadline policy is added
    Then a "defaultWriteWait" const exists alongside "defaultReadWait"
    And a "writeWait" field on Client is defaulted from it in "newClient"
    And tests can shrink "writeWait" before the first Subscribe, and the deadline fires at that shrunk value (~150ms in the AC1-AC4 tests)

  # AC6
  @integration
  Scenario: Existing peerproxy behavior does not regress
    Given the four read-side deadline tests pass on origin/main
    When the write-deadline change is applied on the branch
    Then "go test ./internal/server/providers/peerproxy/ -count=1" reports ok with all tests passing
    And TestSubscribeSilentSocketFailsStreamIntoReconnect, TestSubscribeSilentHandshakeErrorsAndRedials, TestSubscribeSurvivesProtocolPingsThenDelivers, and TestSubscribeSurvivesControlFramesThenDelivers still pass

  # AC7
  @unit
  Scenario: Write-deadline policy is documented
    Given doc.go documents the read-deadline policy and its reconnect role
    When the write-deadline change is applied
    Then doc.go's keepalive prose (or keepalive.go) states the "writeWait" budget with its value and rationale, mirroring "defaultReadWait"
    And the prose states that a write timeout feeds teardown and redial via "failAll"
    And the doc does more than merely mention that a write deadline exists

  # AC8
  @integration
  Scenario: Subscription teardown never parks the write path while holding writeMu
    Given an open subscription on a peer that has stopped reading
    When the subscription's ctx is cancelled and the teardown goroutine writes the "complete" frame
    Then the "complete"-frame write returns within "writeWait" and releases "writeMu"
    And a concurrent Subscribe on the same client returns within 2s rather than blocking on "writeMu"
    And on origin/main this scenario times out from the goroutine or lock leak

# --- AC Coverage Map ---
# AC1: "Write deadline armed before every send; write to a non-reading peer returns net.Error Timeout within writeWait, write path not read path"
#   -> Scenario: Write to a non-reading peer returns a timeout instead of parking
# AC2: "Pong-reply write timeout on an established connection errors the stream via failAll and redials (upgrade counter 1 -> >=2)"
#   -> Scenario: Pong-reply write timeout errors the stream and redials
# AC3: "Subscribe-frame write timeout resets the cached connection so the next Subscribe redials, not reuses the dead conn"
#   -> Scenario: Subscribe-frame write timeout resets the connection instead of stranding it
# AC4: "Healthy writes never falsely timed out; deadline armed fresh per call (sub-writeWait gaps, total > writeWait); mutation-to-cumulative goes red"
#   -> Scenario: Healthy traffic with sub-deadline gaps is never falsely timed out
# AC5: "defaultWriteWait const + writeWait field defaulted in newClient, shrinkable by tests, mirrors readWait; deadline fires at shrunk value"
#   -> Scenario: Write-deadline policy mirrors the read-deadline shape
# AC6: "No regression: full peerproxy suite passes on branch incl. the four read-side deadline tests"
#   -> Scenario: Existing peerproxy behavior does not regress
# AC7: "doc.go/keepalive.go documents the writeWait budget (value + rationale) and its failAll reconnect role"
#   -> Scenario: Write-deadline policy is documented
# AC8: "Teardown complete-frame write returns within writeWait and releases writeMu; concurrent Subscribe not blocked (worst-case deadlock guard)"
#   -> Scenario: Subscription teardown never parks the write path while holding writeMu
#
# Count: 8 issue ACs = 8 mapped scenarios.
#
# Level split rationale:
#   @integration (AC1, AC2, AC3, AC4, AC6, AC8) — exercise the transport end-to-end
#     against a real gorilla websocket over httptest (module boundary, failure handling).
#   @unit (AC5, AC7) — structural/policy invariants on the package's own symbols and docs.
#   No @e2e: a live half-open peer cannot be reproduced on demand; the hermetic httptest
#     harness is the contract surface (same choice as the #732 read-side tests).
