import { expect, test } from "@playwright/test";

const adminEmail =
  process.env.AGINEX_BOOTSTRAP_ADMIN_EMAIL ?? "e2e-admin@example.com";
const adminPassword =
  process.env.AGINEX_BOOTSTRAP_ADMIN_PASSWORD ??
  "correct e2e administrator password";
const apiURL = process.env.NEXT_PUBLIC_API_URL ?? "http://127.0.0.1:8080";
const durableJobsEnabled = process.env.AGINEX_JOBS_DRIVER === "postgres";

test.describe.configure({ mode: "serial" });
test.beforeAll(() => {
  if (process.env.CI && !durableJobsEnabled) {
    throw new Error("CI browser workflows require AGINEX_JOBS_DRIVER=postgres");
  }
});

test("unauthenticated workspace navigation is redirected to sign-in", async ({
  page,
}) => {
  await page.goto("/products");

  await expect(page).toHaveURL(/\/login\?next=%2Fproducts$/);
  await expect(
    page.getByRole("heading", { name: "Sign in to Aginex" }),
  ).toBeVisible();
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
  await expect(page.getByRole("heading", { name: "Products" })).toBeVisible();

  await page.getByRole("button", { name: "Create product" }).click();
  await page.getByLabel("Product name").fill(productName);
  await page.getByLabel("SKU").fill(sku);
  await page.getByLabel("Price").fill("12.34");
  await page.getByLabel("Status").selectOption("active");
  await page
    .getByRole("button", { name: "Create product", exact: true })
    .click();

  const productRow = page.getByRole("row").filter({ hasText: productName });
  await expect(productRow).toContainText(sku);
  await expect(productRow).toContainText("active");

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
  await expect(fileRow).toContainText("ready");
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
