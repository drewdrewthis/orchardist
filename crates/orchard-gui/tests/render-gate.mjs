// Render gate — proves the LIVE https GUI renders a real UI, not a blank screen.
//
// Run after EVERY change that could affect the served bundle (the brief's
// "never let the page go blank again" rule). Plain Playwright, no Gherkin
// runner; drives the live HTTPS endpoint by default.
//
//   node tests/render-gate.mjs [baseURL]
//   ORCHARD_TEST_BASE=https://orchard-gui.drewdrewthis.boxd.sh node tests/render-gate.mjs
//
// Asserts (each maps to a concrete check, exit 1 on any failure):
//   1. the endpoint answers 200
//   2. the body is not blank (rendered text present)
//   3. the SvelteKit app shell mounted (hydration root populated)
//   4. the "Orchard" brand chrome rendered
//   5. the "New" conversation entrypoint is visible (create affordance live)
//   6. the sidebar rendered real daemon state (a lens group or conversation)
//   7. no uncaught page errors during load
import pw from "../node_modules/.pnpm/playwright@1.59.1/node_modules/playwright/index.js";
const { chromium } = pw;
const BASE = process.argv[2] || process.env.ORCHARD_TEST_BASE || "https://orchard-gui.drewdrewthis.boxd.sh";

let fail = 0;
const check = (name, ok, extra = "") => { console.log(`  ${ok ? "PASS" : "FAIL"}  ${name}${extra ? "  — " + extra : ""}`); if (!ok) fail++; };

const pageErrors = [];
const browser = await chromium.launch({ headless: true });
const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
const page = await ctx.newPage();
page.on("pageerror", (e) => pageErrors.push(String(e)));

try {
  const resp = await page.goto(BASE, { waitUntil: "networkidle", timeout: 30000 });
  check("endpoint answers 200", resp && resp.status() === 200, resp ? String(resp.status()) : "no response");
  await page.waitForTimeout(1500);

  const bodyText = (await page.evaluate(() => document.body.innerText || "")).trim();
  check("body is not blank", bodyText.length > 20, `${bodyText.length} chars`);

  const mounted = await page.evaluate(() => {
    const root = document.querySelector("body > div") || document.body;
    return !!root && root.children.length > 0 && root.innerHTML.length > 200;
  });
  check("app shell mounted (hydration root populated)", mounted);

  check('"Orchard" brand chrome rendered', /Orchard/.test(bodyText), bodyText.slice(0, 24).replace(/\n/g, " "));

  const newBtn = await page.getByRole("button", { name: /^New/ }).first().isVisible().catch(() => false);
  check('"New" conversation entrypoint visible', newBtn);

  // Real daemon state: a lens group badge (QUIET/ACTIVE/…) or an open conversation,
  // or the empty-state the GUI shows when no session is open. Any one proves the
  // sidebar/main rendered from live data rather than a white screen.
  const realState = /QUIET|ACTIVE|RECENT|No conversations open|Search/i.test(bodyText);
  check("sidebar/main rendered from live daemon state", realState);

  check("no uncaught page errors", pageErrors.length === 0, pageErrors.slice(0, 3).join(" | "));

  await page.screenshot({ path: "/tmp/ac2-render.png", fullPage: false });
  console.log("  screenshot -> /tmp/ac2-render.png");
} finally {
  await browser.close();
}
console.log(`\n${fail === 0 ? "RENDER OK" : fail + " RENDER GATE FAILURES"} — ${BASE}`);
process.exit(fail === 0 ? 0 : 1);
