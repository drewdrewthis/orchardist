import { defineConfig, devices } from "playwright/test";

/**
 * Phone-shaped Playwright rig pointed at the live `vite dev` server on
 * 5173 + the orchard daemon on 7777. We do NOT spin up a webServer here
 * because the rig must hit the REAL daemon (363 conversations, live
 * subscriptions, real jsonl reads) — that's the contract we're testing.
 *
 * Run the suite with `pnpm playwright test`.
 */

// iPhone 14 viewport (390×844). isMobile + hasTouch are critical: they
// flip Chromium into touch mode (no hover, tap dispatch instead of
// click) which exposes the actual mobile codepath, including the
// MobileLayout branch in store.svelte.ts (innerWidth<768 at construct).
//
// Pinned to Chromium (not the webkit default that ships with
// devices["iPhone 14"]) because the TWA wrapper on the user's phone is
// chromium-backed and the in-repo dev story already only installs
// chromium-1217 (see notifications.spec setup).
const iphone14 = {
	viewport: { width: 390, height: 844 },
	userAgent:
		"Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1",
	isMobile: true,
	hasTouch: true,
	defaultBrowserType: "chromium" as const,
};
void devices;

export default defineConfig({
	testDir: "./tests",
	testMatch: "**/*.spec.ts",
	// Generous timeouts: real daemon reads + jsonl parses for large sessions
	// (8k+ messages on this very session) can spike to 2-3s.
	timeout: 60_000,
	expect: { timeout: 15_000 },
	fullyParallel: false, // single dev server, single daemon — serialize
	workers: 1,
	retries: 0, // surface flakes immediately, don't paper over
	reporter: [["list"], ["html", { open: "never", outputFolder: "playwright-report" }]],
	use: {
		// Tests target the rig server (5273), NOT the user's hot-reload
		// preview (5173). This keeps build hashes from going stale between
		// our edits and the user's running dev session.
		baseURL: process.env.ORCHARD_TEST_BASE ?? "http://127.0.0.1:5273",
		actionTimeout: 15_000,
		navigationTimeout: 30_000,
		trace: "retain-on-failure",
		screenshot: "only-on-failure",
		video: "retain-on-failure",
		// Disable the SW for tests: the production manifest registers a
		// service-worker that aggressively caches the SvelteKit bundle.
		// Playwright tears down per-test contexts which leaves zombie SWs
		// across the rig — and we don't want a stale cached entry serving
		// the previous build to the next test.
		serviceWorkers: "block",
	},
	projects: [
		{
			name: "iPhone 14",
			use: iphone14,
		},
	],
});
