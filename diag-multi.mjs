import { chromium } from "playwright";
const b = await chromium.launch();
const p = await (await b.newContext({ viewport: { width: 1440, height: 900 } })).newPage();
p.on("pageerror", (e) => console.log("PAGE:", e.message));
p.on("console", (m) => { if (m.type() === "error") console.log("err:", m.text().slice(0,200)); });
await p.goto("http://127.0.0.1:1420/", { waitUntil: "domcontentloaded" });
await p.waitForTimeout(3500);
await p.locator(`.lens-pills button[title^="Recent"]`).click();
await p.waitForTimeout(2500);

const items = p.locator(".fleet-list .fleet-item");
console.log("rows:", await items.count());

// Click first row (no modifier)
await items.nth(0).click();
await p.waitForTimeout(1200);
console.log("\n--- after row 0 click ---");
console.log("conv panels:", await p.locator(".conv").count());
console.log("pane elements:", await p.locator(".pane").count());

// Cmd-click second row to open in a new pane (Mac=meta)
await items.nth(1).click({ modifiers: ["Meta"] });
await p.waitForTimeout(1200);
console.log("\n--- after cmd-click row 1 ---");
console.log("conv panels:", await p.locator(".conv").count());
console.log("pane elements:", await p.locator(".pane").count());

// Pane DOM dump
const paneNodes = await p.locator(".pane").all();
for (let i = 0; i < paneNodes.length; i++) {
  const t = await paneNodes[i].innerText().catch(() => "<err>");
  console.log(`pane[${i}] first 200:`, t.slice(0, 200).replace(/\n/g, " | "));
}

// What does the store think it has?
await p.evaluate(() => {
  const w = window;
  // Surface: tabs count via DOM hint.
});
const tabs = await p.locator(".tabbar button, .tab-bar button").count().catch(() => 0);
console.log("\ntabbar buttons:", tabs);

await p.screenshot({ path: "/tmp/orchard-multi.png", fullPage: false });
await b.close();
