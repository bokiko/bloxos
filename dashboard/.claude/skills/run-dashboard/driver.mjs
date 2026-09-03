#!/usr/bin/env node
// Drives a running BloxOS dashboard (localhost:3000) + hub (localhost:4000)
// through first-boot setup and three representative @base-ui/react-backed
// interactions: a dropdown, a modal, and the command palette.
//
// Assumes the hub and dashboard dev server are already running — see
// SKILL.md "Run (agent path)" for the exact launch commands. This script
// only drives the browser; it does not manage the app processes.
//
// Usage: node driver.mjs [--shots-dir <path>]

import { chromium } from "playwright";
import { mkdirSync } from "node:fs";
import path from "node:path";

const BASE = process.env.DASHBOARD_URL || "http://localhost:3000";
const SETUP_TOKEN = process.env.BLOXOS_SETUP_TOKEN || "smoketest-setup-token-12345";
const ADMIN_USER = "smokeadmin";
const ADMIN_PASS = "SmokeTest12345!";

const shotsArgIdx = process.argv.indexOf("--shots-dir");
const SHOTDIR =
  shotsArgIdx !== -1 ? process.argv[shotsArgIdx + 1] : "/tmp/bloxos-dashboard-smoke/screenshots";
mkdirSync(SHOTDIR, { recursive: true });

const consoleErrors = [];
let stepNum = 0;

function log(...args) {
  console.log(...args);
}

async function shot(page, name) {
  stepNum += 1;
  const file = path.join(SHOTDIR, `${String(stepNum).padStart(2, "0")}-${name}.png`);
  await page.screenshot({ path: file });
  log("screenshot:", file);
}

async function main() {
  const browser = await chromium.launch({ args: ["--no-sandbox"] });
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  page.on("console", (msg) => {
    if (msg.type() === "error") consoleErrors.push(msg.text());
  });
  page.on("pageerror", (err) => consoleErrors.push("PAGEERROR: " + err.message));

  // --- First-boot setup ---
  await page.goto(BASE + "/setup", { waitUntil: "networkidle" });
  await shot(page, "setup-page");
  await page.fill('input[placeholder="Paste the one-time setup token"]', SETUP_TOKEN);
  await page.fill('input[placeholder="admin"]', ADMIN_USER);
  await page.fill('input[placeholder="Minimum 8 characters"]', ADMIN_PASS);
  await page.fill('input[placeholder="Repeat password"]', ADMIN_PASS);
  await page.fill('input[placeholder="At least 4 digits"]', "123456");
  await page.fill('input[placeholder="Repeat PIN"]', "123456");
  await shot(page, "setup-filled");
  await page.click('button[type="submit"]');
  await page.waitForTimeout(2000);
  await shot(page, "after-setup-submit");
  log("URL after setup:", page.url());

  // Setup usually auto-signs-in and redirects to /. If instead it lands on
  // /login (e.g. a session cookie didn't take), sign in explicitly.
  if (page.url().includes("/login")) {
    log("landed on /login, signing in manually");
    await page.locator("input").first().fill(ADMIN_USER);
    await page.locator('input[type="password"]').fill(ADMIN_PASS);
    await shot(page, "login-filled");
    await page.click('button[type="submit"]');
    await page.waitForTimeout(2000);
    await shot(page, "after-login");
    log("URL after login:", page.url());
  }

  // --- Fleet page (root) ---
  await page.goto(BASE + "/", { waitUntil: "networkidle" });
  await page.waitForTimeout(1000);
  await shot(page, "fleet-page");

  // --- Dropdown: status filter (base-ui DropdownMenu) ---
  // IMPORTANT: scope the item click to [role="menuitem"], not a bare
  // text= locator. "Live" also appears in the ALERTS summary card on this
  // same page, so text=Live resolves to 2 elements and click() times out
  // fighting the wrong (inert, overlay-covered) one.
  await page.click('button:has-text("All Status")');
  await page.waitForTimeout(300);
  await shot(page, "dropdown-open");
  await page.click('[role="menuitem"]:has-text("Live")');
  await page.waitForTimeout(400);
  await shot(page, "dropdown-selected");
  const filterTrigger = await page.textContent('button:has-text("Live")');
  log("dropdown applied, trigger now reads:", filterTrigger?.trim());

  // --- Modal: Add Machine (base-ui Dialog) ---
  await page.click('button:has-text("Add Machine")');
  await page.waitForTimeout(400);
  await shot(page, "modal-open");
  const modalTitleVisible = await page.isVisible('text="Add Machine"');
  log("modal opened, title visible:", modalTitleVisible);
  await page.keyboard.press("Escape");
  await page.waitForTimeout(300);
  await shot(page, "modal-closed");

  // --- Command palette (⌘K / Ctrl+K) ---
  await page.click('button[aria-label="Open command palette"]');
  await page.waitForTimeout(400);
  await shot(page, "command-palette-open");
  const navigateVisible = await page.isVisible("text=NAVIGATE");
  log("command palette opened, NAVIGATE section visible:", navigateVisible);
  await page.keyboard.press("Escape");

  // --- Agent versions: rollout status must be reachable and legible ---
  await page.goto(BASE + "/versions", { waitUntil: "networkidle" });
  await page.waitForTimeout(500);
  await shot(page, "versions-page");
  const keyPinnedColumnVisible = await page.getByRole("columnheader", { name: "Key pinned" }).isVisible();
  const blockedReasonColumnVisible = await page
    .getByRole("columnheader", { name: "Blocked reason" })
    .isVisible();
  const signingBannerVisible = await page.locator('[data-testid="signing-status-banner"]').isVisible();
  const linuxBinaryVisible = await page.locator('[data-testid="agent-binary-linux"]').isVisible();
  const windowsBinaryVisible = await page.locator('[data-testid="agent-binary-windows"]').isVisible();
  log(
    "versions rendered:",
    `keyPinned=${keyPinnedColumnVisible}`,
    `blockedReason=${blockedReasonColumnVisible}`,
    `signingBanner=${signingBannerVisible}`,
    `linuxBinary=${linuxBinaryVisible}`,
    `windowsBinary=${windowsBinaryVisible}`,
  );

  await browser.close();

  log("");
  log(`=== CONSOLE ERRORS: ${consoleErrors.length} ===`);
  consoleErrors.forEach((e) => log(" -", e));

  if (consoleErrors.length > 0) {
    log("FAIL: console errors were captured during the run");
    process.exit(1);
  }
  if (
    !modalTitleVisible ||
    !navigateVisible ||
    !keyPinnedColumnVisible ||
    !blockedReasonColumnVisible ||
    !signingBannerVisible ||
    !linuxBinaryVisible ||
    !windowsBinaryVisible
  ) {
    log("FAIL: an expected element did not render");
    process.exit(1);
  }
  log("PASS: dashboard smoke test clean");
}

main().catch((err) => {
  console.error("DRIVER FAILED:", err);
  process.exit(1);
});
