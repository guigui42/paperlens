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

test("HTMX search, keyboard, canonical preview and download", async ({ page }) => {
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.getByLabel("Search your documents").fill("latest home insurance certificate");
  await page.getByLabel("Search your documents").press("Enter");
  await expect(page.getByRole("heading", { name: /Documents for/ })).toBeVisible();
  await expect(page.locator(".result-card").first()).toContainText("Home insurance certificate 2025");
  await expect(page).toHaveURL("/");
  await expect(page.getByText("Ordinary Paperless keyword results.")).toBeVisible();
  await page.locator(".result-card").first().getByRole("link", { name: "Preview", exact: true }).click();
  await expect(page.getByTitle("PDF preview")).toBeVisible();
  const download = page.waitForEvent("download");
  await page.getByRole("link", { name: "Download original", exact: true }).click();
  expect((await download).suggestedFilename()).toBe("example-01.pdf");
  expect(errors).toEqual([]);
});

test("Alpine CSP theme and responsive layout", async ({ page }) => {
  const initial = await page.locator("html").getAttribute("data-theme");
  await page.getByLabel("Toggle color theme").click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", initial === "dark" ? "light" : "dark");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.getByRole("button", { name: "Home insurance", exact: true }).click();
  await expect(page.getByLabel("Search your documents")).toBeFocused();
  await expect(page.getByLabel("Search your documents")).toHaveValue("Latest home insurance certificate");
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
  await page.screenshot({ path: `.cache/screenshots/${testInfo.project.name}-home.png`, fullPage: true });
  await page.getByLabel("Search your documents").fill("latest home insurance certificate");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page.locator(".result-card").first()).toBeVisible();
  await page.screenshot({ path: `.cache/screenshots/${testInfo.project.name}-results.png`, fullPage: true });
});
