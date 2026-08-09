import { expect, type Page, test } from "@playwright/test";

const adminEmail =
  process.env.AGINEX_E2E_ADMIN_EMAIL ?? "e2e-admin@example.com";
const adminPassword =
  process.env.AGINEX_E2E_ADMIN_PASSWORD ?? "correct e2e administrator password";
const setupDatabaseDSN =
  process.env.AGINEX_E2E_DATABASE_DSN ??
  "postgres://aginex:aginex@127.0.0.1:5432/aginex?sslmode=disable";
const setupDatabaseDriver =
  process.env.AGINEX_E2E_DATABASE_DRIVER ?? "postgres";
const setupDatabaseEngine = databaseEngineLabel(setupDatabaseDriver);
const setupDatabaseFieldLabel =
  setupDatabaseDriver === "sqlite" ? "Database path" : "Connection string";
const apiURL = process.env.NEXT_PUBLIC_API_URL ?? "http://127.0.0.1:8080";
const durableJobsEnabled = process.env.AGINEX_JOBS_DRIVER === "postgres";

test.describe.configure({ mode: "serial" });
test.beforeAll(() => {
  if (process.env.CI && !durableJobsEnabled) {
    throw new Error("CI browser workflows require AGINEX_JOBS_DRIVER=postgres");
  }
});

test("first-run setup initializes and permanently closes the setup surface", async ({
  page,
}) => {
  const initialMode = await page.request.get(`${apiURL}/api/v1/system/mode`);
  expect(initialMode.ok()).toBe(true);
  const initialModePayload = (await initialMode.json()) as { mode: string };
  expect(initialMode.headers()["cache-control"]).toContain("no-store");
  expect(initialModePayload).toEqual({ mode: "setup" });

  await page.goto("/");
  await expect(page).toHaveURL(/\/setup$/);
  await expect(
    page.getByRole("heading", { name: "Locate the system of record." }),
  ).toBeVisible();

  await verifySetupDatabase(page);

  const oldSetupTab = await page.context().newPage();
  await oldSetupTab.goto("/setup");
  await verifySetupDatabase(oldSetupTab);
  await page.bringToFront();
  await page.getByRole("button", { name: "Continue" }).click();

  await page.getByLabel("Email address").fill(adminEmail);
  await page.getByLabel("Password", { exact: true }).fill(adminPassword);
  await page.getByLabel("Confirm password").fill(adminPassword);
  await page.route(
    "**/api/v1/setup/complete",
    async (route) => {
      await route.fetch();
      await route.abort("failed");
    },
    { times: 1 },
  );
  await page.getByRole("button", { name: "Initialize workspace" }).click();

  await expect(
    page.getByRole("heading", { name: "Bring every service online." }),
  ).toBeVisible();
  await expect(page).toHaveURL(/\/login$/, { timeout: 120_000 });

  const activeMode = await page.request.get(`${apiURL}/api/v1/system/mode`);
  expect(activeMode.ok()).toBe(true);
  expect(await activeMode.json()).toEqual({ mode: "application" });
  expect(activeMode.headers()["cache-control"]).toContain("no-store");

  await assertSetupClosed(page);
  await oldSetupTab.bringToFront();
  await oldSetupTab.evaluate(() => window.dispatchEvent(new Event("focus")));
  await expect(oldSetupTab).toHaveURL(/\/login$/, { timeout: 15_000 });
  await oldSetupTab.close();
});

function databaseEngineLabel(driver: string) {
  switch (driver) {
    case "sqlite":
      return "SQLite";
    case "postgres":
      return "PostgreSQL";
    case "mysql":
      return "MySQL";
    default:
      throw new Error(`unsupported E2E database driver: ${driver}`);
  }
}

async function verifySetupDatabase(page: Page) {
  await page.getByLabel(setupDatabaseEngine).check();
  await page.getByLabel(setupDatabaseFieldLabel).fill(setupDatabaseDSN);
  await page.getByRole("button", { name: "Test connection" }).click();
  await expect(page.getByText("Connection verified")).toBeVisible();
}

async function assertSetupClosed(page: Page) {
  const closedSetup = await page.request.get(`${apiURL}/api/v1/setup/status`);
  expect(closedSetup.status()).toBe(404);
  expect(closedSetup.headers()["cache-control"]).toContain("no-store");

  await page.goto("/setup");
  await expect(page).not.toHaveURL(/\/setup$/);
}

test("unauthenticated workspace navigation is redirected to sign-in", async ({
  page,
}) => {
  await page.goto("/products");

  await expect(page).toHaveURL(/\/login\?next=%2Fproducts$/);
  await expect(
    page.getByRole("heading", { name: "Sign in to Aginex" }),
  ).toBeVisible();
});

test("interface language selection persists without changing routes", async ({
  page,
}) => {
  await page.goto("/login");
  await expect(page.locator("html")).toHaveAttribute("lang", "en");

  await page
    .getByRole("combobox", { name: "Select interface language" })
    .selectOption("zh-CN");

  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  await expect(page.locator("html")).toHaveAttribute("dir", "ltr");
  await expect(
    page.getByRole("heading", { name: "登录 Aginex" }),
  ).toBeVisible();
  await expect(page).toHaveURL(/\/login$/);

  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");
  await expect(
    page.getByRole("heading", { name: "登录 Aginex" }),
  ).toBeVisible();

  const localeCookie = (await page.context().cookies()).find(
    (cookie) => cookie.name === "aginex_locale",
  );
  expect(localeCookie).toMatchObject({ value: "zh-CN", sameSite: "Lax" });
});

test("administrator completes audited product, file, and job workflows", async ({
  page,
}) => {
  const suffix = `${Date.now()}`;
  const productName = `E2E postcard ${suffix}`;
  const sku = `E2E-${suffix}`;
  const imageName = `e2e-pixel-${suffix}.png`;

  await page.goto("/login?next=%2Fproducts");
  await page.getByLabel("Email address").fill(adminEmail);
  await page.getByLabel("Password").fill(adminPassword);
  await page.getByRole("button", { name: "Sign in" }).click();

  await expect(page).toHaveURL(/\/products$/);
  await expect(
    page.getByRole("heading", { name: "Products", exact: true }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Create product" }).click();
  await page.getByLabel("Product name").fill(productName);
  await page.getByLabel("SKU").fill(sku);
  await page.getByLabel("Price").fill("12.34");
  await page.getByLabel("Status").selectOption("active");
  await page
    .locator("form")
    .getByRole("button", { name: "Create product", exact: true })
    .click();

  const productRow = page.getByRole("row").filter({ hasText: productName });
  await expect(productRow).toContainText(sku);
  await expect(productRow).toContainText("Active");

  await page.getByRole("link", { name: "Audit trail" }).click();
  const createAudit = page
    .getByRole("row")
    .filter({ hasText: "products:create" })
    .filter({ hasText: sku });
  await expect(createAudit).toContainText(sku);

  await page.getByRole("link", { name: "Files" }).click();
  await page.locator('input[name="image"]').setInputFiles({
    name: imageName,
    mimeType: "image/png",
    buffer: Buffer.from(
      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
      "base64",
    ),
  });
  await page.getByRole("button", { name: "Upload image" }).click();

  const fileRow = page.getByRole("row").filter({ hasText: imageName });
  await expect(fileRow).toContainText("Ready");
  if (durableJobsEnabled) {
    const deletionResponse = page.waitForResponse(
      (response) =>
        response.request().method() === "DELETE" &&
        /\/api\/v1\/files\/[^/]+$/.test(response.url()),
    );
    await fileRow.getByRole("button", { name: `Delete ${imageName}` }).click();
    expect((await deletionResponse).status()).toBe(202);

    const jobsResponse = await page.request.get(
      `${apiURL}/api/v1/jobs?state=pending&type=storage.cleanup&page=1&pageSize=20`,
    );
    expect(jobsResponse.ok()).toBe(true);
    const jobs = (await jobsResponse.json()) as {
      items: Array<{ state: string; type: string; version: number }>;
    };
    expect(jobs.items).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          state: "pending",
          type: "storage.cleanup",
          version: 2,
        }),
      ]),
    );
  }

  await page.getByRole("link", { name: "Products" }).click();
  const cleanupRow = page.getByRole("row").filter({ hasText: productName });
  await cleanupRow
    .getByRole("button", { name: `Delete ${productName}` })
    .click();
  await expect(cleanupRow).toHaveCount(0);

  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login$/);
});
