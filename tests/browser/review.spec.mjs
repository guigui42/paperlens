import { test, expect } from "@playwright/test";

test("saved receipt navigation preserves browser identity from an external dashboard", async ({ page, context, baseURL }) => {
  await page.goto("/upload");
  await page.getByLabel("Choose document").setInputFiles({
    name: "dashboard-example.pdf", mimeType: "application/pdf",
    buffer: Buffer.from(`%PDF-1.4\n% dashboard ${Date.now()}\n%%EOF\n`)
  });
  await page.getByRole("button", { name: "Upload to Paperless" }).click();
  await expect(page.getByText("Document stored.", { exact: true })).toBeVisible();
  await page.goto("/upload");
  const receipt = await page.locator(".receipt-list a").first().getAttribute("href");
  const before = (await context.cookies()).find(cookie => cookie.name === "paperlens_browser");
  expect(before.sameSite).toBe("Lax");
  await page.route("https://dashboard.example.test/", route => route.fulfill({
    contentType: "text/html", body: `<a href="${new URL(receipt, baseURL)}">Open saved receipt</a>`
  }));
  await page.goto("https://dashboard.example.test/");
  await page.getByRole("link", { name: "Open saved receipt" }).click();
  await expect(page.getByText("Document stored.", { exact: true })).toBeVisible();
  const after = (await context.cookies()).find(cookie => cookie.name === "paperlens_browser");
  expect(after.value).toBe(before.value);
});

test("HTTP errors preserve search targets and later submissions", async ({ page }) => {
  await page.goto("/");
  await page.route("**/search", route => route.fulfill({ status: 403, contentType: "text/plain", body: "Same-origin request required" }), { times: 1 });
  await page.getByLabel("Search your documents").fill("home insurance");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page.locator("#results")).toContainText("Request failed (403). Reload");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page.locator(".result-card").first()).toBeVisible();
});

test("a new search supersedes an obsolete result poll", async ({ page }) => {
  await page.goto("/");
  let admitPoll;
  const pollSeen = new Promise(resolve => { admitPoll = resolve; });
  let finishPoll;
  const pollGate = new Promise(resolve => { finishPoll = resolve; });
  await page.route("**/jobs/*", async route => {
    admitPoll();
    await pollGate;
    if (!route.request().failure()) {
      await route.fulfill({ status: 200, contentType: "text/html", body: '<section id="results">Obsolete results</section>' });
    }
  }, { times: 1 });
  await page.getByLabel("Search your documents").fill("insurance");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await pollSeen;
  const abortedPoll = page.waitForEvent("requestfailed", { predicate: request => new URL(request.url()).pathname.startsWith("/jobs/") });
  await page.getByLabel("Search your documents").fill("washing machine warranty");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await abortedPoll;
  finishPoll();
  await expect(page.getByRole("heading", { name: /Documents for.*washing machine/ })).toBeVisible();
  await expect(page.locator("#results")).not.toContainText("Obsolete results");
});

test("accepted uploads cannot reuse the consumed form for another file", async ({ page }) => {
  await page.goto("/upload");
  await page.getByLabel("Choose document").setInputFiles({
    name: "synthetic.pdf", mimeType: "application/pdf",
    buffer: Buffer.from(`%PDF-1.4\n% review ${Date.now()}\n%%EOF\n`)
  });
  await page.getByRole("button", { name: "Upload to Paperless" }).click();
  await expect(page.getByRole("link", { name: "Choose another document" })).toBeVisible();
  await expect(page.locator("#document-file")).toHaveCount(0);
  await page.getByRole("link", { name: "Choose another document" }).click();
  await expect(page.getByLabel("Choose document")).toBeVisible();
});

test("light and dark text and search controls meet contrast requirements", async ({ page }) => {
  const contrast = async (locator, property = "color") => locator.evaluate((element, property) => {
    const rgb = value => value.match(/[\d.]+/g).slice(0, 3).map(Number);
    const luminance = value => rgb(value).map(v => {
      v /= 255;
      return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
    }).reduce((sum, v, i) => sum + v * [0.2126, 0.7152, 0.0722][i], 0);
    let node = element;
    while (getComputedStyle(node).backgroundColor === "rgba(0, 0, 0, 0)") node = node.parentElement;
    const fg = luminance(getComputedStyle(element)[property]);
    const bg = luminance(getComputedStyle(node).backgroundColor);
    return (Math.max(fg, bg) + 0.05) / (Math.min(fg, bg) + 0.05);
  }, property);
  for (const theme of ["light", "dark"]) {
    await page.goto(`/?scoutTheme=${theme}`);
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
    await page.getByLabel("Search your documents").fill("home insurance");
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await expect(page.getByRole("article").first()).toBeVisible();
    for (const selector of [".privacy", ".result-meta", ".result-excerpt", ".reason", ".search-box button"]) {
      expect(await contrast(page.locator(selector).first()), `${theme}: ${selector}`).toBeGreaterThanOrEqual(4.5);
    }
    expect(await contrast(page.locator(".search-box"), "borderTopColor")).toBeGreaterThanOrEqual(3);
    await page.getByLabel("Search your documents").fill("qzjxvnbpahelqr");
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await expect(page.locator(".empty p")).toBeVisible();
    expect(await contrast(page.locator(".empty p"))).toBeGreaterThanOrEqual(4.5);
    await page.goto(`/upload?scoutTheme=${theme}`);
    expect(await contrast(page.locator(".dropzone span"))).toBeGreaterThanOrEqual(4.5);
  }
});
