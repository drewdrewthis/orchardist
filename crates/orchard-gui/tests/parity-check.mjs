// Parity check — the 1:1 feature-line ↔ test-assertion guarantee.
//
// The brief forbids a heavy Gherkin/cucumber runner; instead each create+chat
// feature has a companion plain-Playwright driver whose assertion calls mirror
// the feature's Then/And lines one-for-one, in order. This script MECHANICALLY
// enforces that: it parses the assertion-bearing lines of each feature and the
// assertion calls of its driver and fails if they don't line up 1:1.
//
//   node tests/parity-check.mjs
//
// A green run guarantees every feature assertion line is backed by exactly one
// concrete driver assertion (no orphan lines, no extra asserts).
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");

// Each pair: a feature and the driver whose `assertFn(...)` calls back it.
const PAIRS = [
  { feature: "features/gui-create-chat.feature", driver: "tests/create-chat.drive.mjs", assertFn: "assert" },
  { feature: "features/gui-render-no-regression.feature", driver: "tests/render-gate.mjs", assertFn: "check" },
];

// Gherkin assertion lines = Then, plus And/But while in the Then phase.
// Given/When (and And/But under them) are setup, not assertions.
function featureAssertions(src) {
  const out = [];
  let phase = null;
  for (const raw of src.split("\n")) {
    const line = raw.trim();
    if (/^(Feature|Background|Scenario(?: Outline)?):/.test(line)) { phase = null; continue; }
    const m = line.match(/^(Given|When|Then|And|But)\s+(.*)$/);
    if (!m) continue;
    const [, kw, text] = m;
    if (kw === "Given" || kw === "When" || kw === "Then") phase = kw;
    if (phase === "Then") out.push(text);
  }
  return out;
}

// Driver assertions = first string-literal argument of each `assertFn(...)`
// call. The arrow-fn definition (`const assert = (then, ok) =>`) is skipped
// because the name there is followed by ` =`, not `(`.
function driverAssertions(src, fn) {
  const out = [];
  const re = new RegExp(`\\b${fn}\\(\\s*(["'\`])((?:\\\\.|(?!\\1).)*)\\1`, "g");
  let m;
  while ((m = re.exec(src))) out.push(m[2]);
  return out;
}

// Content words for comparison: drop ${...} interpolations and Gherkin/English
// stopwords, keep the meaningful tokens. Two lines match when one's content
// words are a subset of the other's — tolerant of paraphrase ("is visible" vs
// "visible") and interpolation ("${BASE}" vs the literal URL), strict about
// the actual subject of the assertion.
const STOP = new Set(["the", "a", "an", "is", "are", "be", "of", "to", "in", "on", "that", "this", "was", "were", "and", "or", "it", "its"]);
const contentWords = (s) =>
  new Set(
    s.replace(/\$\{[^}]*\}/g, " ")
      .toLowerCase()
      .split(/[^a-z0-9]+/)
      .filter((w) => w && !STOP.has(w))
  );
const subset = (a, b) => [...a].every((w) => b.has(w));
// Polarity guard: "no errors" must NOT match "errors". A negation word present
// on one side but not the other is a polarity mismatch even when the content
// words are otherwise a subset — this is why "no" was kept out of STOP above.
const NEG = new Set(["no", "not", "never", "without", "none", "cannot"]);
const isNegative = (s) => s.toLowerCase().split(/[^a-z0-9']+/).some((w) => NEG.has(w) || w.endsWith("n't"));

let failed = 0;
for (const { feature, driver, assertFn } of PAIRS) {
  console.log(`\n${feature}  ↔  ${driver}`);
  const fLines = featureAssertions(readFileSync(resolve(root, feature), "utf8"));
  const dCalls = driverAssertions(readFileSync(resolve(root, driver), "utf8"), assertFn);

  if (fLines.length !== dCalls.length) {
    console.log(`  FAIL  count mismatch: ${fLines.length} feature assertion lines vs ${dCalls.length} ${assertFn}() calls`);
    failed++;
  }
  const n = Math.max(fLines.length, dCalls.length);
  for (let i = 0; i < n; i++) {
    const f = fLines[i] ?? "(missing feature line)";
    const d = dCalls[i] ?? "(missing assertion)";
    const wf = contentWords(f), wd = contentWords(d);
    const polarityOk = isNegative(f) === isNegative(d);
    const ok = wf.size && wd.size && polarityOk && (subset(wf, wd) || subset(wd, wf));
    console.log(`  ${ok ? "PASS" : "FAIL"}  [${i + 1}] ${f}`);
    if (!ok) { console.log(`         ↳ driver: ${d}${polarityOk ? "" : "  [negation-polarity mismatch]"}`); failed++; }
  }
}

console.log(`\n${failed === 0 ? "PARITY OK — every feature line maps 1:1 to a driver assertion" : failed + " PARITY FAILURES"}`);
process.exit(failed === 0 ? 0 : 1);
