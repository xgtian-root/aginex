import { defineConfig, devices } from "@playwright/test";

const webURL = process.env.AGINEX_E2E_WEB_URL ?? "http://127.0.0.1:3000";
const apiURL = process.env.NEXT_PUBLIC_API_URL ?? "http://127.0.0.1:8080";
const webEndpoint = new URL(webURL);
if (
  webEndpoint.protocol !== "http:" ||
  !["127.0.0.1", "localhost"].includes(webEndpoint.hostname) ||
  !/^\d+$/.test(webEndpoint.port || "80")
) {
  throw new Error("AGINEX_E2E_WEB_URL must be a local HTTP origin");
}
const webPort = webEndpoint.port || "80";

export default defineConfig({
  testDir: "./admin/e2e",
  testMatch: "**/*.e2e.ts",
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  // First-run Setup is intentionally one-shot. Retrying against the same API
  // process would exercise the already-configured application and could turn
  // a failed installation attempt into a false-positive test result.
  retries: 0,
  workers: 1,
  reporter: process.env.CI
    ? [["line"], ["html", { open: "never" }]]
    : [["list"]],
  use: {
    baseURL: webURL,
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
    video: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"], locale: "en-US" },
    },
  ],
  webServer: [
    {
      name: "Aginex API",
      command: "go run ./backend/cmd/server",
      url: `${apiURL}/health/ready`,
      reuseExistingServer: !process.env.CI,
      timeout: 120_000,
      gracefulShutdown: { signal: "SIGTERM", timeout: 15_000 },
      stdout: "pipe",
      stderr: "pipe",
    },
    {
      name: "Aginex web",
      command: `pnpm --filter @aginex/admin dev --hostname ${webEndpoint.hostname} --port ${webPort}`,
      url: `${webURL}/login`,
      reuseExistingServer: !process.env.CI,
      timeout: 120_000,
      gracefulShutdown: { signal: "SIGTERM", timeout: 10_000 },
      stdout: "pipe",
      stderr: "pipe",
    },
  ],
});
