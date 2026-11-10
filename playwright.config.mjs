import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/browser",
  timeout: 30000,
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  use: {
    channel: process.env.PAPERLENS_BROWSER_CHANNEL,
    baseURL: "http://127.0.0.1:8099",
    trace: "retain-on-failure",
    screenshot: "only-on-failure"
  },
  projects: [
    { name: "desktop", use: { ...devices["Desktop Chrome"] } },
    { name: "mobile", use: { ...devices["iPhone 13"], defaultBrowserType: "chromium" } }
  ],
  webServer: {
    command: "go run ./cmd/paperlens",
    url: "http://127.0.0.1:8099/healthz",
    reuseExistingServer: false,
    env: {
      PAPERLENS_DEMO: "true",
      PAPERLENS_LISTEN: "127.0.0.1:8099",
      PAPERLENS_ORIGIN: "http://127.0.0.1:8099",
      PAPERLENS_STATE_DIR: ".cache/browser-state"
    }
  }
});
