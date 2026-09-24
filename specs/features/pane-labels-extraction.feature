Feature: pane-labels.sh split into wrapper + Python modules
  As a maintainer of scripts/tmux
  I want the 499-line pane-labels.sh (shell wrapper + embedded Python heredoc) split into
    a thin shell wrapper plus three sibling Python modules (pane_labels.py,
    pane_labels_hookstate.py, pane_labels_fmt.py), each under 300 lines
  So that the tmux pane-labeling script stays maintainable without changing any observable behavior

  Background:
    Given the pre-extraction "pane-labels.sh" at HEAD as the behavioral baseline
    And the extracted files: "pane-labels.sh", "pane_labels.py", "pane_labels_hookstate.py", "pane_labels_fmt.py"

  @unit
  Scenario: Extracted files respect the 300-line ceiling
    When "wc -l" is run on "scripts/tmux/pane-labels.sh", "scripts/tmux/pane_labels.py", "scripts/tmux/pane_labels_hookstate.py", and "scripts/tmux/pane_labels_fmt.py"
    Then every file reports fewer than 300 lines

  @integration
  Scenario: Existing bats suite passes unchanged after extraction
    When "bats scripts/tmux/pane-labels.bats scripts/tmux/orchard-tmux.bats" is run
    Then every test reports "ok" with 0 failures
    And the set of "@test" names in "pane-labels.bats" is identical to the set at HEAD

  @integration
  Scenario Outline: "--print" output is byte-identical to the pre-extraction baseline
    Given daemon reachability "<daemon>" and "ORCHARD_LABEL_ENRICH" set to "<enrich>"
    When the new "pane-labels.sh --print" is run against "<fixture>"
    And the HEAD "pane-labels.sh --print" is run against the same "<fixture>" with the same options
    Then a diff of the two outputs is empty

    Examples:
      | fixture                                | daemon    | enrich  |
      | daemon-response.json                   | reachable | unset   |
      | daemon-response.json                   | reachable | 1       |
      | daemon-response-evil.json               | reachable | unset   |
      | daemon-response-evil.json               | reachable | 1       |
      | daemon-response-malformed.json          | reachable | unset   |
      | daemon-response-malformed.json          | reachable | 1       |
      | daemon-response.json                   | unreachable | unset |
      | daemon-response.json                   | unreachable | 1     |

  @integration
  Scenario: Sibling modules resolve without PYTHONPATH from any working directory
    Given a working directory outside "scripts/tmux"
    And "PYTHONPATH" is unset
    When "bash /abs/scripts/tmux/pane-labels.sh --daemon-url http://127.0.0.1:9 --heartbeat-dir /tmp --panes-file <fixture> --print" is run
    Then the process exits 0
    And stdout contains the fixture's expected labels
    And stderr contains no "ModuleNotFoundError"

  @integration
  Scenario: Real tmux pane option matches "--print" label after a live, non-print render
    Given a throwaway tmux server on socket "i769" with at least 2 panes
    When "pane-labels.sh" is run against those panes without "--print"
    Then "tmux -L i769 show-options -pv -t <pane> @orchard_pane_label" equals the "--print" label for each pane
    And the throwaway tmux server is killed afterward

  @integration
  Scenario: Panes remain labelled from local data when the daemon is unreachable
    Given an unreachable "--daemon-url"
    When "pane-labels.sh --print" is run against a panes fixture
    Then every pane label is non-empty, derived from local path, Claude state, or process data
    And the process exits 0

  @integration
  Scenario: Provenance bats tests remain green after extraction
    When the "pane-labels.bats" provenance tests are run
    Then "world-writable heartbeat dir refused" passes
    And "foreign-owner sidecar skipped" passes
    And "symlink sidecar skipped" passes
    And "world-writable sidecar skipped" passes

  @integration
  Scenario Outline: Sidecar trust gates the Claude state cell in "--print" output
    Given a manually planted "orchard-claude-<sess>.json" sidecar that is "<trust>"
    When "pane-labels.sh --print" is run against the pane
    Then the Claude state cell is "<expected>"

    Examples:
      | trust     | expected |
      | untrusted | absent   |
      | trusted   | present  |

  @integration
  Scenario: Plugin binding bats suite passes, including the apostrophe-path picker test
    When "bats scripts/tmux/orchard-tmux.bats" is run
    Then every test reports "ok" including "apostrophe in the labeler path still binds the picker"

  @unit
  Scenario: Fixture copy list includes both extracted Python siblings
    When "scripts/tmux/orchard-tmux.bats" line 163 is inspected
    Then the "cp" invocation copies "pane_labels.py", "pane_labels_hookstate.py", and "pane_labels_fmt.py" alongside "pane-labels.sh"

  @unit
  Scenario: README lists all four sibling files
    When "scripts/tmux/README.md" is inspected
    Then it lists "orchard.tmux", "pane-labels.sh", "pane_labels.py", "pane_labels_hookstate.py", and "pane_labels_fmt.py"
    And it no longer states that the directory holds exactly "two files"

  @unit
  Scenario: Shell wrapper and Python modules compile cleanly
    When "bash -n scripts/tmux/pane-labels.sh" is run
    Then it exits 0
    When "python3 -m py_compile" is run on "pane_labels.py", "pane_labels_hookstate.py", and "pane_labels_fmt.py"
    Then each exits 0

# --- AC Coverage Map ---
# AC 1: "File sizes" (wc -l all < 300) → Scenario: Extracted files respect the 300-line ceiling
# AC 2: "Bats suite unchanged & green" → Scenario: Existing bats suite passes unchanged after extraction
# AC 3: "Byte-identical --print output" → Scenario Outline: "--print" output is byte-identical to the pre-extraction baseline
# AC 4: "Sibling resolution with no PYTHONPATH, from any cwd" → Scenario: Sibling modules resolve without PYTHONPATH from any working directory
# AC 5: "Live render, real tmux, non-print path" → Scenario: Real tmux pane option matches "--print" label after a live, non-print render
# AC 6: "Daemon-unreachable regression" → Scenario: Panes remain labelled from local data when the daemon is unreachable
# AC 7: "Sidecar provenance regression" → Scenario: Provenance bats tests remain green after extraction; Scenario Outline: Sidecar trust gates the Claude state cell in "--print" output
# AC 8: "Plugin binding + apostrophe degradation" → Scenario: Plugin binding bats suite passes, including the apostrophe-path picker test; Scenario: Fixture copy list includes both extracted Python siblings
# AC 9: "README reflects the new files" → Scenario: README lists all four sibling files
# AC 10: "Shell syntax clean" → Scenario: Shell wrapper and Python modules compile cleanly
