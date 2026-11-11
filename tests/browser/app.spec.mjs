import { test, expect } from "@playwright/test";
import fs from "node:fs/promises";

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    window.cspViolations = [];
    document.addEventListener("securitypolicyviolation", event => window.cspViolations.push(event.violatedDirective));
  });
  const response = await page.goto("/");
  expect(response.headers()["referrer-policy"]).toBe("same-origin");
});

test.afterEach(async ({ page }) => {
  expect(await page.evaluate(() => window.cspViolations)).toEqual([]);
});

test("HTMX search, lazy thumbnails, inline preview, canonical page and download", async ({ page }) => {
  const errors = [];
  const previewRequests = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("request", request => {
    if (/\/documents\/\d+\/preview$/.test(new URL(request.url()).pathname)) previewRequests.push(request.url());
  });
  await page.getByLabel("Search your documents").fill("latest home insurance certificate");
  await page.getByLabel("Search your documents").press("Enter");
  await expect(page.getByRole("heading", { name: /Documents for/ })).toBeVisible();
  await expect(page.locator(".result-card").first()).toContainText("Home insurance certificate 2025");
  await expect(page).toHaveURL("/?mode=keyword&q=latest+home+insurance+certificate");
  await expect(page.getByText("Ordinary Paperless keyword results.")).toBeVisible();
  const card = page.getByRole("article").first();
  await expect.poll(() => card.locator("[data-thumbnail]").evaluate(image => image.complete && image.naturalWidth > 0)).toBe(true);
  expect(previewRequests).toHaveLength(0);
  const preview = card.getByRole("heading").getByRole("link");
  await expect(card.getByRole("link", { name: /^Preview / })).toHaveCount(1);
  const mediaResponse = page.waitForResponse(response => new URL(response.url()).pathname === "/documents/1/preview");
  await preview.focus();
  await preview.press("Enter");
  await expect(card.getByRole("region")).toBeVisible();
  await expect(card.getByTitle("PDF preview")).toBeVisible();
  const pdf = await mediaResponse;
  expect(pdf.status()).toBe(200);
  expect(pdf.headers()["content-type"]).toBe("application/pdf");
  // Chromium's native PDF viewer owns the navigation body, outside Playwright's response cache.
  const documentBytes = await page.request.get(pdf.url());
  expect((await documentBytes.body()).subarray(0, 4).toString()).toBe("%PDF");
  await expect(page).toHaveURL("/?mode=keyword&q=latest+home+insurance+certificate");
  await expect(page.getByLabel("Search your documents")).toHaveValue("latest home insurance certificate");
  await card.getByRole("button", { name: /^Close preview of/ }).click();
  await expect(card.getByRole("region")).toBeHidden();
  await expect(card.locator("iframe")).toHaveCount(0);
  await expect(preview).toBeFocused();
  const reopened = page.waitForResponse(response => new URL(response.url()).pathname === "/documents/1/preview");
  await card.locator(".result-thumbnail").click();
  await reopened;
  expect(previewRequests.length).toBeGreaterThanOrEqual(2);
  await card.getByRole("link", { name: "Open full page", exact: true }).click();
  await expect(page).toHaveURL("/documents/1");
  await expect(page.getByTitle("PDF preview")).toBeVisible();
  const download = page.waitForEvent("download");
  await page.getByRole("link", { name: "Download original", exact: true }).click();
  expect((await download).suggestedFilename()).toBe("example-01.pdf");
  expect(errors).toEqual([]);
});

test("preview controls have one keyboard stop and isolated announcements", async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route("**/documents/1", async route => {
    const response = await route.fetch();
    await gate;
    await route.fulfill({ response });
  }, { times: 1 });
  await page.getByLabel("Search your documents").fill("home insurance");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  const card = page.getByRole("article").first();
  const title = card.getByRole("heading").getByRole("link");
  try {
    await expect(title).toHaveAttribute("aria-expanded", "false");
    const searchStatus = page.locator("#search-status");
    await expect(searchStatus).toContainText("documents shown");
    const announcement = await searchStatus.textContent();
    await expect(card.getByRole("link", { name: /^Preview / })).toHaveCount(1);
    await title.focus();
    await page.keyboard.press("Tab");
    await expect(card.getByRole("link", { name: "Download original", exact: true })).toBeFocused();
    await title.focus();
    await page.keyboard.press("Shift+Tab");
    await expect(page.locator("[data-recent] summary")).toBeFocused();
    await title.focus();
    await page.keyboard.press("Enter");
    await expect(card.getByRole("status")).toHaveText("Loading document preview...");
    expect(await card.getByRole("status").evaluate(element =>
      element.parentElement.closest('[aria-live], [role="status"], [role="alert"]') === null
    )).toBe(true);
    await expect(searchStatus).toHaveText(announcement);
    await expect(page).toHaveURL(/\/\?mode=keyword&q=/);
    release();
    await expect(card.getByTitle("PDF preview")).toBeVisible();
    await card.getByRole("button", { name: /^Close preview of/ }).click();
    await expect(title).toBeFocused();
    await expect(title).toHaveAttribute("aria-expanded", "false");
    await expect(searchStatus).toHaveText(announcement);
  } finally {
    release();
  }
});

test("result titles remain ordinary document links without JavaScript", async ({ page, browser }) => {
  const poll = page.waitForResponse(response => /\/jobs\/[^/]+$/.test(new URL(response.url()).pathname));
  await page.getByLabel("Search your documents").fill("home insurance");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page.getByRole("article").first()).toBeVisible();
  const context = await browser.newContext({ javaScriptEnabled: false, storageState: await page.context().storageState() });
  try {
    const plainPage = await context.newPage();
    await plainPage.goto((await poll).url());
    const card = plainPage.getByRole("article").first();
    const title = card.getByRole("heading").getByRole("link");
    await expect(card.locator("[aria-expanded], [aria-controls]")).toHaveCount(0);
    await title.click();
    await expect(plainPage).toHaveURL(/\/documents\/1$/);
    await expect(plainPage.getByRole("link", { name: "Download original", exact: true })).toBeVisible();
  } finally {
    await context.close();
  }
});

test("thumbnail and inline preview failures stay inside the result", async ({ page }) => {
  await page.route("**/documents/1/thumbnail", route => route.fulfill({ status: 404, body: "Thumbnail missing" }));
  await page.route("**/documents/1", route => {
    expect(route.request().headers()["x-paperlens-preview"]).toBe("inline");
    return route.fulfill({ status: 502, body: "Upstream unavailable" });
  }, { times: 1 });
  await page.getByLabel("Search your documents").fill("home insurance");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  const card = page.getByRole("article").first();
  await expect(card.getByText("Preview unavailable", { exact: true })).toBeVisible();
  await card.locator(".result-thumbnail").click();
  await expect(card.getByRole("alert")).toContainText("Preview could not load (HTTP 502)");
  await expect(card.getByRole("link", { name: "Download original", exact: true })).toBeVisible();
  await expect(page.getByRole("article")).toHaveCount(4);
  await card.getByRole("button", { name: /^Close preview of/ }).click();
  await card.locator(".result-thumbnail").click();
  await expect(card.getByTitle("PDF preview")).toBeVisible();
  await page.getByLabel("Search your documents").fill("washing machine warranty");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page.getByRole("heading", { name: /Documents for.*washing machine/ })).toBeVisible();
  await expect(page.locator("iframe")).toHaveCount(0);
  await page.getByRole("article").first().locator(".result-thumbnail").click();
  await expect(page.getByTitle("PDF preview")).toBeVisible();
});

for (const interruption of ["collapse", "new search"]) {
  test(`a late inline preview cannot survive ${interruption}`, async ({ page }) => {
    let admit;
    const requestSeen = new Promise(resolve => { admit = resolve; });
    let release;
    const gate = new Promise(resolve => { release = resolve; });
    await page.route("**/documents/1", async route => {
      admit();
      await gate;
      if (!route.request().failure()) {
        await route.fulfill({ contentType: "text/html", body: "<p>Stale document preview</p>" });
      }
    }, { times: 1 });
    await page.getByLabel("Search your documents").fill("home insurance");
    await page.getByRole("button", { name: "Search", exact: true }).click();
    const card = page.getByRole("article").first();
    await card.locator(".result-thumbnail").click();
    await requestSeen;
    const aborted = page.waitForEvent("requestfailed", {
      predicate: request => new URL(request.url()).pathname === "/documents/1"
    });
    try {
      if (interruption === "collapse") {
        await card.getByRole("button", { name: /^Close preview of/ }).click();
      } else {
        await page.getByLabel("Search your documents").fill("washing machine warranty");
        await page.getByRole("button", { name: "Search", exact: true }).click();
      }
      await aborted;
    } finally {
      release();
    }
    if (interruption === "new search") {
      await expect(page.getByRole("heading", { name: /Documents for.*washing machine/ })).toBeVisible();
    }
    await expect(page.getByText("Stale document preview", { exact: true })).toHaveCount(0);
    await expect(page.locator(".inline-preview:visible")).toHaveCount(0);
    await page.getByRole("article").first().locator(".result-thumbnail").click();
    await expect(page.getByTitle("PDF preview")).toBeVisible();
  });
}

test("long OCR stays compact and narrow layouts do not overflow", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.getByLabel("Search your documents").fill("home insurance");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  const card = page.getByRole("article").first();
  await expect(card).toBeVisible();
  const bounds = await card.boundingBox();
  expect(bounds.y + bounds.height).toBeLessThanOrEqual(900);
  await expect(page.locator(".intro .lede")).toBeHidden();
  const quote = "A line of original OCR with some evidence.\n\n\n\n".repeat(12);
  await card.locator(".result-excerpt").evaluate((element, text) => { element.textContent = text; }, quote);
  const excerpt = card.locator(".result-excerpt");
  for (const width of [1280, 320]) {
    await page.setViewportSize({ width, height: 900 });
    const metrics = await excerpt.evaluate(element => ({
      height: element.getBoundingClientRect().height,
      lineHeight: Number.parseFloat(getComputedStyle(element).lineHeight)
    }));
    expect(metrics.height).toBeLessThanOrEqual(metrics.lineHeight * 3 + 1);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  }
  await card.locator(".result-thumbnail").click();
  expect(await excerpt.textContent()).toBe(quote);
  const expandedHeight = await excerpt.evaluate(element => element.getBoundingClientRect().height);
  expect(expandedHeight).toBeGreaterThan(200);
  await card.getByRole("button", { name: /^Close preview of/ }).click();
  await page.goto("/upload");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test("inline previews still work after results pagination", async ({ page }) => {
  await page.getByLabel("Search your documents").fill("insurance warranty bill receipt registration tax newsletter document");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  const firstTitle = page.getByRole("article").first().getByRole("heading");
  await expect(firstTitle).toBeVisible();
  const originalTitle = await firstTitle.textContent();
  await page.getByRole("article").first().locator(".result-thumbnail").click();
  await expect(page.getByTitle("PDF preview")).toBeVisible();
  await page.getByRole("navigation", { name: "Results pages" }).getByRole("link", { name: "Next", exact: true }).click();
  await expect(firstTitle).not.toHaveText(originalTitle);
  await expect(page.locator("iframe")).toHaveCount(0);
  await expect(firstTitle.getByRole("link")).toHaveAttribute("aria-expanded", "false");
  await firstTitle.getByRole("link").click();
  await expect(page.getByTitle("PDF preview")).toBeVisible();
  await page.getByRole("navigation", { name: "Results pages" }).getByRole("link", { name: "Previous", exact: true }).click();
  await expect(firstTitle).toHaveText(originalTitle);
  await expect(page.locator("iframe")).toHaveCount(0);
  await expect(page).toHaveURL("/?mode=keyword&q=insurance+warranty+bill+receipt+registration+tax+newsletter+document");
});

test("uploaded image previews render inline and expose media failures", async ({ page }, testInfo) => {
  const image = await page.request.get("/documents/1/thumbnail");
  const title = `imageproof${Date.now()}${testInfo.project.name}`;
  await page.goto("/upload");
  await page.getByLabel("Choose document").setInputFiles({
    name: "synthetic.png", mimeType: "image/png",
    buffer: Buffer.concat([await image.body(), Buffer.from(title)])
  });
  await page.getByLabel("Title").fill(title);
  await page.getByRole("button", { name: "Upload to Paperless" }).click();
  await expect(page.getByText("Document stored.", { exact: true })).toBeVisible();
  const documentURL = await page.getByRole("link", { name: "Preview document", exact: true }).getAttribute("href");
  await page.goto("/");
  await page.getByLabel("Search your documents").fill(title);
  await page.getByRole("button", { name: "Search", exact: true }).click();
  const card = page.getByRole("article").first();
  await card.locator(".result-thumbnail").click();
  const previewImage = card.getByRole("img", { name: `Preview of ${title}` });
  await expect.poll(() => previewImage.evaluate(element => element.naturalWidth)).toBe(220);
  await expect(card.locator("iframe")).toHaveCount(0);
  await card.getByRole("button", { name: /^Close preview of/ }).click();
  await page.route(`**${documentURL}/preview`, route => route.fulfill({ status: 502, body: "Synthetic image failure" }), { times: 1 });
  await card.locator(".result-thumbnail").click();
  await expect(card.getByRole("alert")).toContainText("The image could not load.");
  await expect(card.getByRole("link", { name: "Download original", exact: true })).toBeVisible();
  await card.getByRole("button", { name: /^Close preview of/ }).click();
  await card.locator(".result-thumbnail").click();
  await expect.poll(() => previewImage.evaluate(element => element.naturalWidth)).toBe(220);
});

test("Alpine CSP theme and responsive layout", async ({ page }) => {
  const initial = await page.locator("html").getAttribute("data-theme");
  await page.getByLabel("Toggle color theme").click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", initial === "dark" ? "light" : "dark");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await expect(page.getByRole("button", { name: "Home insurance", exact: true })).toHaveCount(0);
  await expect(page.locator(".result-card")).toHaveCount(0);
});

test("immutable date and person filters", async ({ page }) => {
  await page.getByText("Refine your search", { exact: true }).click();
  await page.getByLabel("Issued before").fill("2024-12-31");
  await page.getByLabel("Search your documents").fill("home insurance");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page.locator(".result-card")).toHaveCount(1);
  await expect(page.locator(".result-card")).toContainText("Home insurance certificate 2024");
  await page.getByLabel("Issued before").fill("");
  await page.getByLabel("Custom field", { exact: true }).selectOption("1");
  await page.getByLabel("Exact custom value").fill("Alex Example");
  await page.getByLabel("Search your documents").fill("school registration");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page.locator(".result-card")).toHaveCount(1);
  await expect(page.locator(".result-card")).toContainText("School registration");
});

test("cancel, empty and upstream error states", async ({ page }) => {
  await page.getByLabel("Search your documents").fill("insurance");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await page.getByRole("button", { name: "Cancel search" }).click();
  await expect(page.getByText("Search cancelled. No further retrieval will run.")).toBeVisible();
  await page.getByLabel("Search your documents").fill("astronaut mars passport");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page.getByRole("heading", { name: "No matching evidence found." })).toBeVisible();
  await page.getByLabel("Search your documents").fill("simulate upstream error");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("synthetic upstream unavailable");
});

test("excluded documents are clearly non-AI and OCR is escaped", async ({ page }) => {
  await page.getByLabel("Search your documents").fill("financial export");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page.getByText("Not sent to AI")).toBeVisible();
  await expect(page.locator(".result-card").filter({ hasText: "Not sent to AI" }).locator(".reason")).toContainText("Excluded from AI");
});

test("upload ingestion, duplicate and failed outcomes", async ({ page }, testInfo) => {
  await page.getByRole("link", { name: "Upload document", exact: true }).click();
  const buffer = Buffer.from(`%PDF-1.4\n% synthetic ${testInfo.project.name} ${Date.now()}\n%%EOF\n`);
  await page.getByLabel("Choose document").setInputFiles({ name: "example.pdf", mimeType: "application/pdf", buffer });
  await page.getByLabel("Title").fill("Synthetic browser upload");
  await page.getByRole("button", { name: "Upload to Paperless" }).click();
  await expect(page.getByText("Accepted. Paperless is processing your file.")).toBeVisible();
  await expect(page.getByText("Document stored.", { exact: true })).toBeVisible();
  await page.getByRole("link", { name: "Upload another" }).click();
  await page.getByLabel("Choose document").setInputFiles({ name: "example.pdf", mimeType: "application/pdf", buffer });
  await page.getByRole("button", { name: "Upload to Paperless" }).click();
  await expect(page.getByText("Duplicate detected by Paperless.")).toBeVisible();
  await page.goto("/upload");
  await page.getByLabel("Choose document").setInputFiles({ name: "failure.pdf", mimeType: "application/pdf", buffer });
  await page.getByLabel("Title").fill("simulate failure");
  await page.getByRole("button", { name: "Upload to Paperless" }).click();
  await expect(page.getByText("Ingestion failed.", { exact: true })).toBeVisible();
});

test("reject disguised uploads before sending", async ({ page }) => {
  await page.goto("/upload");
  await page.getByLabel("Choose document").setInputFiles({ name: "malicious.pdf", mimeType: "application/pdf", buffer: Buffer.from("<script>alert(1)</script>") });
  await page.getByRole("button", { name: "Upload to Paperless" }).click();
  await expect(page.getByRole("alert")).toContainText("contents do not match");
});

test("synthetic screenshot artifacts", async ({ page }, testInfo) => {
  await fs.mkdir(".cache/screenshots", { recursive: true });
  for (const theme of ["light", "dark"]) {
    await page.emulateMedia({ colorScheme: theme });
    await page.goto(`/?scoutTheme=${theme}`);
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
    const prefix = `.cache/screenshots/${testInfo.project.name}-${theme}`;
    await page.screenshot({ path: `${prefix}-home.png`, fullPage: true });
    await page.getByLabel("Search your documents").fill("latest home insurance certificate");
    await page.getByRole("button", { name: "Search", exact: true }).click();
    const card = page.getByRole("article").first();
    await expect.poll(() => card.locator("[data-thumbnail]").evaluate(image => image.naturalWidth)).toBeGreaterThan(0);
    await page.screenshot({ path: `${prefix}-results.png`, fullPage: true });
    await card.locator(".result-thumbnail").click();
    await expect(card.getByTitle("PDF preview")).toBeVisible();
    await card.screenshot({ path: `${prefix}-expanded.png` });
    await card.getByRole("link", { name: "Open full page", exact: true }).click();
    await expect(page.getByTitle("PDF preview")).toBeVisible();
    await page.screenshot({ path: `${prefix}-document.png`, fullPage: true });
    await page.goto("/upload");
    await page.screenshot({ path: `${prefix}-upload.png`, fullPage: true });
    await page.getByLabel("Choose document").setInputFiles({
      name: "synthetic-ui-example.pdf", mimeType: "application/pdf",
      buffer: Buffer.from(`%PDF-1.4\n% synthetic UI ${testInfo.project.name} ${theme} ${Date.now()}\n%%EOF\n`)
    });
    await page.getByRole("button", { name: "Upload to Paperless" }).click();
    await expect(page.getByText("Document stored.", { exact: true })).toBeVisible();
    await page.goto("/upload");
    await page.locator(".receipt-list a").first().click();
    await expect(page.getByText("Document stored.", { exact: true })).toBeVisible();
    await page.screenshot({ path: `${prefix}-receipt.png`, fullPage: true });
  }
});

test("search URLs reload, history and recent searches", async ({ page, context }) => {
  const posts = [];
  page.on("request", request => { if (request.method() === "POST" && new URL(request.url()).pathname === "/search") posts.push(request); });
  await expect(page.getByText("Try a search")).toHaveCount(0);
  const recent = page.locator("[data-recent]");
  await expect(recent).toContainText("No recent searches yet.");
  const search = async query => {
    await page.getByLabel("Search your documents").fill(query);
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await expect(page.locator("[data-credits-label]")).toHaveText("0 AI credits (no AI used)");
  };
  await search("home insurance");
  await expect(page).toHaveURL("/?mode=keyword&q=home+insurance");
  await search("electricity bill");
  await search("home insurance");
  await expect(recent).not.toHaveAttribute("open", "");
  await recent.locator("summary").click();
  const links = recent.locator(".recent-link");
  await expect(links).toHaveCount(2);
  await expect(links.first()).toContainText("home insurance");
  await expect(links.first()).toContainText("0 AI credits (no AI used)");
  await expect(links.first()).toHaveAttribute("href", "/?mode=keyword&q=home+insurance");

  await page.goBack();
  await expect(page).toHaveURL("/?mode=keyword&q=electricity+bill");
  await expect(page.getByLabel("Search your documents")).toHaveValue("electricity bill");
  await expect(page.locator("[data-history-restored]")).toBeVisible();
  await expect(page.locator(".result-card")).toHaveCount(0);
  expect(posts).toHaveLength(3);

  posts.length = 0;
  await page.reload();
  await expect(page.locator("[data-credits-label]")).toBeVisible();
  await expect(page).toHaveURL("/?mode=keyword&q=electricity+bill");
  expect(posts).toHaveLength(1);
  expect(posts[0].headers()["hx-request"]).toBe("true");

  const other = await context.newPage();
  await other.goto("/");
  await expect(other.locator(".recent-link")).toHaveCount(2);
  await other.evaluate(() => {
    const items = Array.from({ length: 12 }, (_, i) => ({ url: "/?mode=keyword&q=" + encodeURIComponent(i === 0 ? "<img src=x onerror=alert(1)>" : "q" + i), at: Date.now() - i, credits: "0 AI credits (no AI used)", status: "none" }));
    items.push({ url: "https://evil.example/?q=x" }, { url: "javascript:alert(1)" });
    localStorage.setItem("paperlens.recentSearches.v1", JSON.stringify({ v: 1, items }));
  });
  await other.reload();
  await expect(other.locator(".recent-link")).toHaveCount(10);
  await expect(other.locator(".recent-query").first()).toHaveText("<img src=x onerror=alert(1)>");
  await expect(other.locator("[data-recent] img")).toHaveCount(0);
  await other.getByRole("button", { name: "Clear history" }).click();
  await expect(other.locator(".recent-link")).toHaveCount(0);
  await expect(other.locator("[data-recent]")).toContainText("No recent searches yet.");
  expect(await other.evaluate(() => localStorage.getItem("paperlens.recentSearches.v1"))).toBeNull();
  await other.close();
});

test("failed URL autorun does not replace the next successful history entry", async ({ page }) => {
  let first = true;
  const posts = [];
  page.on("request", request => {
    if (request.method() === "POST" && new URL(request.url()).pathname === "/search") posts.push(request);
  });
  await page.route("**/search", async route => {
    if (first) {
      first = false;
      await route.abort();
      return;
    }
    await route.continue();
  });
  await page.goto("/?mode=keyword&q=home+insurance");
  await expect.poll(() => posts.length).toBe(1);
  await page.getByLabel("Search your documents").fill("electricity bill");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page).toHaveURL("/?mode=keyword&q=electricity+bill");
  await expect(page.locator("[data-credits-label]")).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL("/?mode=keyword&q=home+insurance");
  await expect(page.locator("[data-history-restored]")).toBeVisible();
  expect(posts).toHaveLength(2);
});

test("invalid search links do not run", async ({ page }) => {
  let posted = false;
  page.on("request", request => { if (request.method() === "POST") posted = true; });
  await page.goto("/?q=x&tag=99999");
  await expect(page.getByText(/This search link has invalid filters/)).toBeVisible();
  await page.waitForTimeout(300);
  expect(posted).toBe(false);
});
