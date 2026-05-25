Feature: GUI renders without regression on the live HTTPS endpoint
  As a user who can open https://orchard-gui.drewdrewthis.boxd.sh at any time
  I want the page to always render a real UI after a server bring-up or change
  So that the create+chat work never lands the GUI on a blank/white screen.

  This is the "never let the page go blank again" gate. Every Then/And line
  below maps 1:1 to one logged check in the companion driver
  `tests/render-gate.mjs` (a plain Playwright script — no Gherkin runner), so a
  green run proves this file line-for-line. Run it after every change that
  touches the served bundle, and as the no-regression tail of the create+chat
  suite.

  Background:
    Given the gui-servers tmux session is up (daemon :7777 + vite preview :4173)
    And a desktop-width browser loads the live HTTPS URL

  @live
  Scenario: The live GUI renders a real UI, not a blank screen
    When the browser navigates to https://orchard-gui.drewdrewthis.boxd.sh
    Then the endpoint answers 200
    And the body is not blank (rendered text present)
    And the app shell mounted (hydration root populated)
    And the "Orchard" brand chrome rendered
    And the "New" conversation entrypoint is visible
    And the sidebar/main rendered from live daemon state
    And there are no uncaught page errors during load
