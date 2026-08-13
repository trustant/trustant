// screenshot.mjs — capture one frame of the running app (see spec/16-screenshot.md).
//
// Driven by ../screenshot.sh; usage: node tests/screenshot.mjs <url> <out.png>
//
// Uses the Playwright library API re-exported by @playwright/test rather than
// `playwright test`: a one-shot capture needs no spec file, reporter, or
// test-results/ directory.
import { chromium } from "@playwright/test";

const [url, out] = process.argv.slice(2);
if (!url || !out) {
  console.error("usage: screenshot.mjs <url> <out.png>");
  process.exit(2);
}

// Fixed viewport, and fullPage stays false. Both are load-bearing: every frame
// of an animation must share the same dimensions, and a full-page shot grows
// and shrinks with the page content.
const width = Number(process.env.TRUSTABLE_SCREENSHOT_WIDTH || 600);
const height = Number(process.env.TRUSTABLE_SCREENSHOT_HEIGHT || 800);

// --no-sandbox matches tests/playwright.config.mjs: the VM and the pod both run
// as root, where Chromium's sandbox refuses to start.
const browser = await chromium.launch({ headless: true, args: ["--no-sandbox"] });
try {
  const page = await browser.newPage({
    viewport: { width, height },
    deviceScaleFactor: 1,
  });
  await page.goto(url, { waitUntil: "networkidle", timeout: 60_000 });
  // networkidle means the requests stopped, not that the paint settled. A short
  // pause lets fonts and CSS transitions land so frames are not caught midway.
  await page.waitForTimeout(1000);
  await page.screenshot({ path: out, fullPage: false });
} finally {
  // Always close: a leaked Chromium keeps running after the loop moves on.
  await browser.close();
}
