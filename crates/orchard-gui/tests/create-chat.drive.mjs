// Companion driver for features/gui-create-chat.feature.
//
// Plain Playwright script (NO Gherkin runner). Each assert() call mirrors one
// Then/And line in the feature, in order, so a green run proves the feature
// scenario-for-scenario. Drives the LIVE HTTPS endpoint by default.
//
//   node tests/create-chat.drive.mjs [baseURL]
//   ORCHARD_TEST_BASE=https://orchard-gui.drewdrewthis.boxd.sh node tests/create-chat.drive.mjs
//
// Spawns a REAL Claude session (named orchardist*) and kills it on exit —
// that is why this is a .drive.mjs, not a **/*.spec.ts the rig auto-runs.
//
// playwright's default export is its CJS module ({ chromium, firefox, … }).
import pw from "playwright";
import { execSync } from "node:child_process";
const { chromium } = pw;

const BASE = process.argv[2] || process.env.ORCHARD_TEST_BASE || "https://orchard-gui.drewdrewthis.boxd.sh";
const MARK = "PONGORCHARD";
const MSG = `Reply with exactly this one word and nothing else: ${MARK}`;
const tmux = (c) => { try { return execSync(c, { encoding: "utf8" }); } catch { return ""; } };

let failures = 0;
let scenario = "";
const SCENARIO = (s) => { scenario = s; console.log(`\nScenario: ${s}`); };
const assert = (then, ok) => {
  console.log(`  ${ok ? "PASS" : "FAIL"}  Then ${then}`);
  if (!ok) failures++;
};

const before = new Set(tmux("tmux ls -F '#{session_name}' 2>/dev/null").split("\n").filter(Boolean));
const pageErrors = [];
let createdSession = null;

const browser = await chromium.launch({ headless: true });
const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
const page = await ctx.newPage();
page.on("pageerror", (e) => pageErrors.push(String(e)));
// Capture live-update evidence: graphql-ws frames. The conversationChanged
// subscription's "next" frames carry the field name in their JSON payload, so
// a frame matching /conversationChanged/ proves the WS subscription delivered.
// Listener attaches before goto so the handshake + every frame is observed.
const wsFrames = [];
page.on("websocket", (ws) => {
  ws.on("framereceived", (f) => { try { wsFrames.push(String(f.payload)); } catch {} });
});

try {
  await page.goto(BASE, { waitUntil: "networkidle", timeout: 30000 });
  await page.waitForTimeout(1200);

  // ── Scenario: Create a real Claude session ────────────────────────────────
  SCENARIO("Create a real Claude session from the New Conversation modal");
  await page.getByRole("button", { name: /^New/ }).first().click();
  await page.getByText("Launch new conversation").waitFor({ timeout: 5000 });
  await page.getByRole("button", { name: /Launch/ }).last().click();
  await page.waitForFunction(() => /Launched/i.test(document.body.innerText), { timeout: 15000 }).catch(() => {});
  const toast = await page.evaluate(() => (document.body.innerText.match(/Launched[^\n]*/i) || [""])[0]);
  assert(`a success toast "Launched <sessionName>" is shown`, /^Launched\s+\S/.test(toast));
  // Bind cleanup + the new-session check to the EXACT name from the toast — a
  // generic before/after diff could pick up (and later kill) an unrelated
  // session created concurrently by something else.
  const launchedName = (toast.match(/^Launched\s+(\S+)/) || [])[1] || null;
  await page.waitForTimeout(1500);
  const after = new Set(tmux("tmux ls -F '#{session_name}' 2>/dev/null").split("\n").filter(Boolean));
  createdSession = launchedName && after.has(launchedName) && !before.has(launchedName) ? launchedName : null;
  assert("a new tmux session appears that was not present before the click", !!createdSession);
  const paneLine = createdSession ? tmux(`tmux list-panes -t ${createdSession} -F '#{pane_id} #{pane_current_command}'`).trim() : "";
  const paneId = paneLine.split(" ")[0] || "";
  // Claude may still be exec'ing at first capture; poll briefly.
  let paneIsClaude = false;
  for (let i = 0; i < 8 && !paneIsClaude; i++) {
    if (tmux(`tmux list-panes -t '${createdSession}' -F '#{pane_current_command}'`).includes("claude")) paneIsClaude = true;
    else await page.waitForTimeout(1000);
  }
  assert("that session's pane is running the claude command", paneIsClaude);

  // ── Scenario: usable chat composer ────────────────────────────────────────
  SCENARIO("A browser-created session opens a usable chat composer");
  const composer = page.locator("textarea[placeholder^='Message']");
  const composerVisible = await composer.isVisible().catch(() => false);
  assert(`the message composer textarea (placeholder "Message…") is visible`, composerVisible);
  const blocked = await page.getByText(/No tmux pane resolved|desktop app required/i).isVisible().catch(() => false);
  assert(`no "No tmux pane resolved" or "desktop app required" placeholder is shown`, !blocked);

  // ── Scenario: send submits to the pane ────────────────────────────────────
  SCENARIO("Sending a message submits it to the pane (sendTextToPane)");
  await page.waitForTimeout(8000); // let the fresh REPL finish booting
  await composer.click();
  await composer.fill(MSG);
  await page.keyboard.press("Enter");
  await page.waitForTimeout(800);
  const cleared = (await composer.inputValue()) === "";
  assert("the textarea clears instantly (optimistic, before the mutation resolves)", cleared);
  let submitted = false;
  for (let i = 0; i < 16 && !submitted; i++) {
    const cap = tmux(`tmux capture-pane -t '${paneId}' -p 2>/dev/null`);
    // Submitted = Claude started processing (message no longer just sitting at the ❯ prompt).
    if (/(✻|✶|●|Crunch|Sauté|Churn|Discombobulat|Reply with exactly)/.test(cap) && (cap.match(/Reply with exactly/g) || []).length >= 1) {
      // confirm submit by checking Claude moved past the bare prompt (reply token or thinking spinner)
      if (/(●|✻|✶|tokens|thinking|esc to interrupt)/i.test(cap)) submitted = true;
    }
    if (!submitted) await page.waitForTimeout(1500);
  }
  assert("the message text reaches the target tmux pane and is submitted to Claude", submitted);

  // ── Scenario: reply streams into transcript ───────────────────────────────
  SCENARIO("Claude's reply streams into the transcript without a manual refresh");
  let assistantRendered = false;
  for (let i = 0; i < 40 && !assistantRendered; i++) {
    assistantRendered = await page.evaluate((mark) =>
      Array.from(document.querySelectorAll('[data-role="assistant"]')).some((t) => (t.innerText || "").includes(mark)),
      MARK);
    if (!assistantRendered) await page.waitForTimeout(1500);
  }
  const convChangedFrames = wsFrames.filter((f) => /conversationChanged/.test(f)).length;
  console.log(`    (ws frames received: ${wsFrames.length}; conversationChanged frames: ${convChangedFrames})`);
  assert("conversationChanged fires over the WebSocket and the transcript re-fetches", convChangedFrames > 0);
  assert(`an assistant turn ([data-role="assistant"]) containing the reply renders in the GUI`, assistantRendered);

  // ── Scenario: end-to-end ──────────────────────────────────────────────────
  SCENARIO("End-to-end create → chat → reply entirely on the live HTTPS URL");
  assert(`the whole flow completes from ${BASE}`, !!createdSession && assistantRendered);
  assert("there are no uncaught page errors during the flow", pageErrors.length === 0);
  if (pageErrors.length) console.log("    pageErrors:", pageErrors.slice(0, 5));

  await page.screenshot({ path: "/tmp/create-chat-drive.png" });
} finally {
  await browser.close();
  if (createdSession) { tmux(`tmux kill-session -t '${createdSession}' 2>/dev/null`); console.log(`\n(cleaned up test session ${createdSession})`); }
}

console.log(`\n${failures === 0 ? "ALL GREEN" : failures + " FAILED"} — ${BASE}`);
process.exit(failures === 0 ? 0 : 1);
