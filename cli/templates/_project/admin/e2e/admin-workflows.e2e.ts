import { createHash } from "node:crypto";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, type Page, type Route, test } from "@playwright/test";

const adminEmail =
  process.env.AGINEX_E2E_ADMIN_EMAIL ?? "e2e-admin@example.com";
const adminPassword =
  process.env.AGINEX_E2E_ADMIN_PASSWORD ?? "correct e2e administrator password";
const setupDatabaseDriver =
  process.env.AGINEX_E2E_DATABASE_DRIVER ?? "postgres";
const setupDatabaseEngine = databaseEngineLabel(setupDatabaseDriver);
const setupDatabaseDirectory =
  process.env.AGINEX_E2E_DATABASE_DIRECTORY ?? "data";
const setupDatabaseFilename =
  process.env.AGINEX_E2E_DATABASE_FILENAME ?? "aginex.db";
const setupDatabaseHost = process.env.AGINEX_E2E_DATABASE_HOST ?? "127.0.0.1";
const setupDatabasePort =
  process.env.AGINEX_E2E_DATABASE_PORT ??
  (setupDatabaseDriver === "mysql" ? "3306" : "5432");
const setupDatabaseName = process.env.AGINEX_E2E_DATABASE_NAME ?? "aginex";
const setupDatabaseUsername =
  process.env.AGINEX_E2E_DATABASE_USERNAME ?? "aginex";
const setupDatabasePassword =
  process.env.AGINEX_E2E_DATABASE_PASSWORD ?? "aginex";
const setupDatabaseSSLMode =
  process.env.AGINEX_E2E_DATABASE_SSL_MODE ?? "disable";
const setupDatabaseTLSMode =
  process.env.AGINEX_E2E_DATABASE_TLS_MODE ?? "disabled";
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
  switch (setupDatabaseDriver) {
    case "sqlite":
      await page.getByLabel("Database directory").fill(setupDatabaseDirectory);
      await page.getByLabel("Database file name").fill(setupDatabaseFilename);
      break;
    case "postgres":
      await fillServerDatabaseFields(page);
      await page.getByLabel("SSL mode").selectOption(setupDatabaseSSLMode);
      break;
    case "mysql":
      await fillServerDatabaseFields(page);
      await page.getByLabel("TLS mode").selectOption(setupDatabaseTLSMode);
      break;
    default:
      throw new Error(
        `unsupported E2E database driver: ${setupDatabaseDriver}`,
      );
  }
  await page.getByRole("button", { name: "Test connection" }).click();
  await expect(page.getByText("Connection verified")).toBeVisible();
}

async function fillServerDatabaseFields(page: Page) {
  await page.getByLabel("Host", { exact: true }).fill(setupDatabaseHost);
  await page.getByLabel("Port", { exact: true }).fill(setupDatabasePort);
  await page.getByLabel("Database name").fill(setupDatabaseName);
  await page.getByLabel("Username").fill(setupDatabaseUsername);
  await page.getByLabel("Database password").fill(setupDatabasePassword);
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

test("administrator completes audited resource and access workflows", async ({
  page,
}) => {
  const suffix = `${Date.now()}`;
  const productName = `E2E postcard ${suffix}`;
  const sku = `E2E-${suffix}`;
  const uploadFixtures = genericUploadFixtures(suffix);
  const imageName = uploadFixtures[0].name;

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

  await test.step("transfer and safely retrieve a generic file batch", async () => {
    await completeGenericFileWorkflow(page, uploadFixtures);
  });

  const fileRow = page.getByRole("row").filter({ hasText: imageName });
  await expect(fileRow).toContainText("Ready");
  if (durableJobsEnabled) {
    const deletionResponse = page.waitForResponse(
      (response) =>
        response.request().method() === "DELETE" &&
        /\/api\/v1\/files\/[^/]+$/.test(response.url()),
    );
    await fileRow.getByRole("button", { name: `Delete ${imageName}` }).click();
    await page.getByRole("button", { name: "Delete file" }).click();
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
          version: 3,
        }),
      ]),
    );
  }

  await test.step("resume a large transfer from its last acknowledged part", async () => {
    await completeResumableFileWorkflow(page, suffix);
  });

  await page.getByRole("link", { name: "System settings" }).click();
  await expect(
    page.getByRole("heading", { name: "Object storage", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Local storage", exact: true }),
  ).toBeVisible();

  await page.getByRole("link", { name: "Products" }).click();
  const cleanupRow = page.getByRole("row").filter({ hasText: productName });
  await cleanupRow
    .getByRole("button", { name: `Delete ${productName}` })
    .click();
  await expect(cleanupRow).toHaveCount(0);

  await test.step("manage a scoped role and its assigned person", async () => {
    await completeAccessManagementWorkflow(page, suffix);
  });

  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login$/);
});

type UploadFixture = {
  name: string;
  mimeType: string;
  buffer: Buffer;
};

function genericUploadFixtures(suffix: string): UploadFixture[] {
  return [
    {
      name: `e2e-pixel-${suffix}.png`,
      mimeType: "image/png",
      buffer: Buffer.from(
        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
        "base64",
      ),
    },
    {
      name: `e2e-guide-${suffix}.pdf`,
      mimeType: "application/pdf",
      buffer: Buffer.from(
        "%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\nxref\n0 2\n0000000000 65535 f \n0000000009 00000 n \ntrailer\n<< /Size 2 /Root 1 0 R >>\nstartxref\n45\n%%EOF\n",
      ),
    },
    {
      name: `e2e-notes-${suffix}.txt`,
      mimeType: "text/plain",
      buffer: Buffer.from("A plain-text file belongs in the same batch.\n"),
    },
    {
      name: `e2e-page-${suffix}.html`,
      mimeType: "text/html",
      buffer: Buffer.from(
        "<!doctype html><html><body><script>window.e2eUnsafe = true</script></body></html>",
      ),
    },
    {
      name: `e2e-vector-${suffix}.svg`,
      mimeType: "image/svg+xml",
      buffer: Buffer.from(
        '<svg xmlns="http://www.w3.org/2000/svg"><script>window.e2eUnsafe = true</script></svg>',
      ),
    },
  ];
}

async function completeGenericFileWorkflow(
  page: Page,
  fixtures: UploadFixture[],
) {
  const initialInventory = page.waitForResponse(isFileInventoryResponse);
  await page.getByRole("link", { name: "Files" }).click();
  expect((await initialInventory).ok()).toBe(true);

  const policyResponse = await page.request.get(
    `${apiURL}/api/v1/files/upload-policy`,
  );
  expect(policyResponse.ok()).toBe(true);
  expect(await policyResponse.json()).toMatchObject({
    resumableUploadsEnabled: false,
    multipartThresholdBytes: 32 * 1024 * 1024,
  });
  await expect(
    page.getByText("Direct transfer", { exact: true }),
  ).toBeVisible();

  await page.locator('input[name="files"]').setInputFiles(fixtures);
  const ledger = page.getByRole("list", { name: "Staged transfer files" });
  for (const fixture of fixtures) {
    const transfer = ledger.getByRole("listitem").filter({
      hasText: fixture.name,
    });
    await expect(transfer).toContainText("Ready to start");
    await expect(transfer).toContainText("Direct");
    await expect(
      transfer.getByRole("button", {
        name: `Pause ${fixture.name} immediately`,
      }),
    ).toHaveCount(0);
  }

  let inventoryRefreshes = 0;
  const countInventoryRefresh = (request: {
    method(): string;
    url(): string;
  }) => {
    if (isFileInventoryRequest(request)) inventoryRefreshes += 1;
  };
  page.on("request", countInventoryRefresh);
  await page
    .getByRole("button", { name: `Start ${fixtures.length} transfers` })
    .click();

  for (const fixture of fixtures) {
    const transfer = ledger.getByRole("listitem").filter({
      hasText: fixture.name,
    });
    await expect(transfer).toContainText("Verified", { timeout: 120_000 });
    await expect(
      transfer.getByRole("progressbar", {
        name: `Upload progress for ${fixture.name}`,
      }),
    ).toHaveJSProperty("value", 100);
  }
  await expect(
    page.getByText(`${fixtures.length} files uploaded and verified.`),
  ).toBeVisible();
  for (const fixture of fixtures) {
    await expect(fileInventoryRow(page, fixture.name)).toContainText("Ready");
  }
  page.off("request", countInventoryRefresh);
  expect(inventoryRefreshes).toBe(1);

  await page.evaluate(() => {
    const testWindow = window as Window & { __aginexOpenedURLs?: string[] };
    testWindow.__aginexOpenedURLs = [];
    window.open = (url) => {
      testWindow.__aginexOpenedURLs?.push(String(url));
      return null;
    };
  });

  const [image, pdf, text, html, svg] = fixtures;
  await verifyInlineImage(page, image);
  await verifySandboxedPDF(page, pdf);
  await expect(
    fileInventoryRow(page, text.name).getByRole("button", {
      name: `Download ${text.name}`,
    }),
  ).toBeVisible();
  await verifyForcedDownload(page, html);
  await verifyForcedDownload(page, svg);
}

const multipartPartSize = 32 * 1024 * 1024;

type MockCompletedPart = {
  partNumber: number;
  size: number;
  confirmedAt: string;
};

async function completeResumableFileWorkflow(page: Page, suffix: string) {
  const fileName = `e2e-resume-${suffix}.bin`;
  const fileSize = multipartPartSize + 1024 * 1024 + 137;
  const source = Buffer.alloc(fileSize, 0xa5);
  source.write("aginex resumable e2e", 0, "utf8");
  source.writeUInt32BE(fileSize, source.length - 4);
  const expectedSHA256 = createHash("sha256").update(source).digest("hex");
  const fixtureDirectory = await mkdtemp(join(tmpdir(), "aginex-e2e-upload-"));
  const fixturePath = join(fixtureDirectory, fileName);
  await writeFile(fixturePath, source);

  const sessionID = "10000000-0000-4000-8000-000000000001";
  const fileID = "20000000-0000-4000-8000-000000000001";
  const timestamp = "2099-08-11T00:00:00Z";
  const expiresAt = "2099-08-12T00:00:00Z";
  const completedParts: MockCompletedPart[] = [];
  const uploadedParts = new Map<number, Buffer>();
  const partUploadCounts = new Map<number, number>();
  const signedAfterReload: number[] = [];
  let expectedFingerprint = "";
  let resumedFingerprint = "";
  let finalSHA256 = "";
  let sessionCreated = false;
  let recoveryMode = false;
  let crashCaptured = false;
  let completed = false;
  let resolveFirstAck: () => void;
  const firstAck = new Promise<void>((resolve) => {
    resolveFirstAck = resolve;
  });

  const filePayload = (status: "pending" | "ready") => ({
    id: fileID,
    originalName: fileName,
    contentType: "application/octet-stream",
    size: fileSize,
    provider: "local",
    sha256: status === "ready" ? finalSHA256 : "",
    previewKind: "none",
    status,
    visibility: "private",
    width: 0,
    height: 0,
    createdAt: timestamp,
    updatedAt: timestamp,
  });
  const sessionPayload = () => ({
    id: sessionID,
    file: filePayload("pending"),
    status: "active",
    partSize: multipartPartSize,
    partCount: 2,
    completedParts: completedParts.map((part) => ({ ...part })),
    expiresAt,
    createdAt: timestamp,
    updatedAt: timestamp,
  });

  const routeHandler = async (route: Route) => {
    const request = route.request();
    const url = new URL(request.url());
    const { pathname } = url;
    const method = request.method();

    if (method === "GET" && pathname === "/api/v1/files/upload-policy") {
      await fulfillJSON(route, {
        maxUploadBytes: 1024 * 1024 * 1024,
        resumableUploadsEnabled: true,
        resumableAvailable: true,
        multipartThresholdBytes: multipartPartSize,
        multipartPartSizeBytes: multipartPartSize,
        sessionTtlSeconds: 86_400,
        maxBatchFiles: 20,
      });
      return;
    }
    if (method === "GET" && pathname === "/api/v1/files") {
      const items = completed ? [filePayload("ready")] : [];
      await fulfillJSON(route, {
        items,
        page: 1,
        pageSize: 20,
        total: items.length,
      });
      return;
    }
    if (method === "GET" && pathname === "/api/v1/files/upload-sessions") {
      expect(url.searchParams.get("state")).toBe("incomplete");
      const items = sessionCreated && !completed ? [sessionPayload()] : [];
      await fulfillJSON(route, {
        items,
        page: 1,
        pageSize: 20,
        total: items.length,
      });
      return;
    }
    if (method === "POST" && pathname === "/api/v1/files/upload-intents") {
      const body = request.postDataJSON() as {
        filename: string;
        size: number;
        strategy: string;
        resumeFingerprint: string;
      };
      expect(body).toMatchObject({
        filename: fileName,
        size: fileSize,
        strategy: "resumable",
      });
      expect(body.resumeFingerprint).toMatch(/^[a-f0-9]{64}$/);
      expectedFingerprint = body.resumeFingerprint;
      sessionCreated = true;
      await fulfillJSON(route, {
        strategy: "resumable",
        file: filePayload("pending"),
        session: sessionPayload(),
      });
      return;
    }
    if (
      method === "POST" &&
      pathname === `/api/v1/files/upload-sessions/${sessionID}/resume`
    ) {
      const body = request.postDataJSON() as { fingerprint: string };
      resumedFingerprint = body.fingerprint;
      if (resumedFingerprint !== expectedFingerprint) {
        await fulfillProblem(route, 409, "UPLOAD_FINGERPRINT_MISMATCH");
        return;
      }
      await fulfillJSON(route, sessionPayload());
      return;
    }
    if (
      method === "POST" &&
      pathname === `/api/v1/files/upload-sessions/${sessionID}/parts/sign`
    ) {
      const body = request.postDataJSON() as { partNumbers: number[] };
      if (recoveryMode) signedAfterReload.push(...body.partNumbers);
      await fulfillJSON(route, {
        items: body.partNumbers.map((partNumber) => ({
          partNumber,
          size:
            partNumber === 1 ? multipartPartSize : fileSize - multipartPartSize,
          upload: {
            method: "PUT",
            url: `/api/v1/files/upload-sessions/${sessionID}/parts/${partNumber}?mock-signature=current`,
            headers: { "Content-Type": "application/octet-stream" },
            expiresAt,
          },
        })),
      });
      return;
    }
    if (
      method === "PUT" &&
      pathname.startsWith(`/api/v1/files/upload-sessions/${sessionID}/parts/`)
    ) {
      const partNumber = Number(pathname.split("/").at(-1));
      const bytes = request.postDataBuffer();
      expect(bytes).not.toBeNull();
      uploadedParts.set(partNumber, Buffer.from(bytes as Buffer));
      partUploadCounts.set(
        partNumber,
        (partUploadCounts.get(partNumber) ?? 0) + 1,
      );
      await route.fulfill({
        status: 200,
        headers: { ETag: `"part-${partNumber}"` },
        body: "",
      });
      return;
    }
    if (
      method === "POST" &&
      pathname === `/api/v1/files/upload-sessions/${sessionID}/parts/ack`
    ) {
      const body = request.postDataJSON() as {
        parts: Array<{ partNumber: number; etag: string }>;
      };
      if (!recoveryMode) {
        if (!crashCaptured) {
          expect(body.parts.map((part) => part.partNumber)).toEqual([1, 2]);
          completedParts.splice(0, completedParts.length, {
            partNumber: 1,
            size: multipartPartSize,
            confirmedAt: timestamp,
          });
          crashCaptured = true;
          resolveFirstAck();
        }
        await route.abort("failed");
        return;
      }
      for (const part of body.parts) {
        expect(part.etag).toBe(`"part-${part.partNumber}"`);
        if (
          !completedParts.some(
            (completedPart) => completedPart.partNumber === part.partNumber,
          )
        ) {
          completedParts.push({
            partNumber: part.partNumber,
            size:
              part.partNumber === 1
                ? multipartPartSize
                : fileSize - multipartPartSize,
            confirmedAt: timestamp,
          });
        }
      }
      completedParts.sort((left, right) => left.partNumber - right.partNumber);
      await fulfillJSON(route, sessionPayload());
      return;
    }
    if (
      method === "POST" &&
      pathname === `/api/v1/files/upload-sessions/${sessionID}/complete`
    ) {
      const first = uploadedParts.get(1);
      const second = uploadedParts.get(2);
      if (!first || !second || completedParts.length !== 2) {
        await fulfillProblem(route, 409, "UPLOAD_PARTS_INCOMPLETE");
        return;
      }
      finalSHA256 = createHash("sha256")
        .update(first)
        .update(second)
        .digest("hex");
      completed = true;
      await fulfillJSON(route, filePayload("ready"));
      return;
    }
    await route.fallback();
  };

  await page.route("**/api/v1/files**", routeHandler);
  try {
    await page.reload();
    await expect(
      page.getByText("Resumable over 32 MiB", { exact: true }),
    ).toBeVisible();
    await page.locator('input[name="files"]').setInputFiles(fixturePath);
    const ledger = page.getByRole("list", { name: "Staged transfer files" });
    const transfer = ledger.getByRole("listitem").filter({ hasText: fileName });
    await expect(transfer).toContainText("Resumable");
    await page.getByRole("button", { name: "Start 1 transfers" }).click();

    await firstAck;
    expect(completedParts.map((part) => part.partNumber)).toEqual([1]);
    recoveryMode = true;
    await page.reload();

    const recovery = page
      .locator(".recovery-list li")
      .filter({ hasText: fileName });
    await expect(recovery).toContainText("1 of 2 parts confirmed");
    await recovery.locator('input[type="file"]').setInputFiles(fixturePath);
    await expect(
      page.getByText(`${fileName} matched. Only unconfirmed parts are queued.`),
    ).toBeVisible();
    await page.getByRole("button", { name: "Start 1 transfers" }).click();

    const resumedTransfer = ledger
      .getByRole("listitem")
      .filter({ hasText: fileName });
    await expect(resumedTransfer).toContainText("Verified", {
      timeout: 120_000,
    });
    expect(resumedFingerprint).toBe(expectedFingerprint);
    expect(signedAfterReload).toEqual([2]);
    expect(partUploadCounts.get(1)).toBe(1);
    expect(partUploadCounts.get(2)).toBe(2);
    expect(finalSHA256).toBe(expectedSHA256);
    await expect(fileInventoryRow(page, fileName)).toContainText("Ready");
  } finally {
    await page.unroute("**/api/v1/files**", routeHandler);
    await rm(fixtureDirectory, { recursive: true, force: true });
  }
}

async function fulfillJSON(route: Route, body: unknown) {
  await route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(body),
  });
}

async function fulfillProblem(route: Route, status: number, code: string) {
  await route.fulfill({
    status,
    contentType: "application/problem+json",
    body: JSON.stringify({
      type: "about:blank",
      title: "Mock resumable upload failed",
      status,
      detail: code,
      code,
    }),
  });
}

async function verifyInlineImage(page: Page, fixture: UploadFixture) {
  const signedResponse = page.waitForResponse((response) =>
    isFileURLResponse(response, "preview"),
  );
  await fileInventoryRow(page, fixture.name)
    .getByRole("button", { name: `Preview ${fixture.name}` })
    .click();
  const signed = await readSignedURL(await signedResponse);
  await expectLastOpenedURL(page, signed);

  const content = await page.request.get(signed);
  expect(content.ok()).toBe(true);
  expect(content.headers()["content-type"]).toBe("image/png");
  expect(content.headers()["content-disposition"]).toMatch(/^inline;/);
  expect(content.headers()["x-content-type-options"]).toBe("nosniff");
  expect(await content.body()).toEqual(fixture.buffer);
}

async function verifySandboxedPDF(page: Page, fixture: UploadFixture) {
  const signedResponse = page.waitForResponse((response) =>
    isFileURLResponse(response, "preview"),
  );
  await fileInventoryRow(page, fixture.name)
    .getByRole("button", { name: `Preview ${fixture.name}` })
    .click();
  const signed = await readSignedURL(await signedResponse);
  const preview = page.getByTitle(`Preview ${fixture.name}`);
  await expect(preview).toBeVisible();
  await expect(preview).toHaveAttribute("sandbox", "");
  await expect(preview).toHaveAttribute("src", signed);

  const content = await page.request.get(signed);
  expect(content.ok()).toBe(true);
  expect(content.headers()["content-type"]).toBe("application/pdf");
  expect(content.headers()["content-disposition"]).toMatch(/^inline;/);
  expect(content.headers()["content-security-policy"]).toBe("sandbox");
  expect(content.headers()["x-content-type-options"]).toBe("nosniff");
  expect(await content.body()).toEqual(fixture.buffer);

  await page.getByRole("button", { name: "Close PDF preview" }).click();
  await expect(preview).toHaveCount(0);
}

async function verifyForcedDownload(page: Page, fixture: UploadFixture) {
  const row = fileInventoryRow(page, fixture.name);
  await expect(
    row.getByRole("button", { name: `Preview ${fixture.name}` }),
  ).toHaveCount(0);
  const signedResponse = page.waitForResponse((response) =>
    isFileURLResponse(response, "download"),
  );
  await row.getByRole("button", { name: `Download ${fixture.name}` }).click();
  const signed = await readSignedURL(await signedResponse);
  await expectLastOpenedURL(page, signed);

  const content = await page.request.get(signed);
  expect(content.ok()).toBe(true);
  expect(content.headers()["content-type"]).toBe("application/octet-stream");
  expect(content.headers()["content-disposition"]).toMatch(/^attachment;/);
  expect(content.headers()["content-disposition"]).toContain(
    "filename*=UTF-8''",
  );
  expect(content.headers()["x-content-type-options"]).toBe("nosniff");
  expect(await content.body()).toEqual(fixture.buffer);
}

function fileInventoryRow(page: Page, name: string) {
  return page
    .getByRole("row")
    .filter({ hasText: name })
    .filter({ hasText: "Ready" });
}

function isFileInventoryRequest(request: { method(): string; url(): string }) {
  const url = new URL(request.url());
  return request.method() === "GET" && url.pathname === "/api/v1/files";
}

function isFileInventoryResponse(response: {
  request(): { method(): string; url(): string };
}) {
  return isFileInventoryRequest(response.request());
}

function isFileURLResponse(
  response: { request(): { method(): string }; url(): string },
  purpose: "preview" | "download",
) {
  const url = new URL(response.url());
  return (
    response.request().method() === "GET" &&
    /^\/api\/v1\/files\/[^/]+\/url$/.test(url.pathname) &&
    url.searchParams.get("purpose") === purpose
  );
}

async function readSignedURL(response: {
  ok(): boolean;
  json(): Promise<unknown>;
}) {
  expect(response.ok()).toBe(true);
  const payload = (await response.json()) as { url?: unknown };
  expect(typeof payload.url).toBe("string");
  return payload.url as string;
}

async function expectLastOpenedURL(page: Page, expected: string) {
  await expect
    .poll(() =>
      page.evaluate(() => {
        const urls = (window as Window & { __aginexOpenedURLs?: string[] })
          .__aginexOpenedURLs;
        return urls?.at(-1);
      }),
    )
    .toBe(expected);
}

async function completeAccessManagementWorkflow(page: Page, suffix: string) {
  const permissionCode = "products:read";
  const roleName = `E2E product reader ${suffix}`;
  const personName = `E2E reader ${suffix}`;
  const personEmail = `e2e-reader-${suffix}@example.com`;
  const personPassword = `correct e2e reader password ${suffix}`;

  await page.getByRole("link", { name: "Access" }).click();
  await expect(
    page.getByRole("heading", { name: "Access", exact: true }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Create role" }).click();
  await page.getByLabel("Role name").fill(roleName);
  await page
    .getByLabel("Description", { exact: true })
    .fill("Reads the product catalog without write access.");
  await page.getByLabel("Search permissions").fill(permissionCode);

  const permissionOption = page
    .locator("label.permission-option")
    .filter({ hasText: permissionCode });
  await expect(permissionOption).toBeVisible();
  await permissionOption.getByRole("checkbox").check();
  await permissionOption
    .getByRole("combobox", { name: `Scope for ${permissionCode}` })
    .selectOption("all");

  const roleCreation = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      /\/api\/v1\/roles$/.test(response.url()),
  );
  await page
    .locator("form")
    .getByRole("button", { name: "Create role", exact: true })
    .click();
  const roleCreationResponse = await roleCreation;
  expect(roleCreationResponse.status()).toBe(201);
  expect(await roleCreationResponse.json()).toMatchObject({
    name: roleName,
    grants: [
      {
        permission: { code: permissionCode },
        scope: "all",
      },
    ],
  });

  const roleRow = page.getByRole("row").filter({ hasText: roleName });
  await expect(roleRow).toContainText("1 grant");
  await expect(roleRow).toContainText("0 people");

  await page.getByRole("link", { name: "People" }).click();
  await expect(
    page.getByRole("heading", { name: "People", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Create person" }).click();
  await page.getByLabel("Display name").fill(personName);
  await page.getByLabel("Email address", { exact: true }).fill(personEmail);
  await page.getByLabel("Password", { exact: true }).fill(personPassword);
  await page.getByLabel("Confirm password").fill(personPassword);

  const roleOption = page
    .locator("label.choice-card")
    .filter({ hasText: roleName });
  await expect(roleOption).toBeVisible();
  await roleOption.getByRole("checkbox").check();
  await page
    .locator("form")
    .getByRole("button", { name: "Create person", exact: true })
    .click();

  const personRow = page.getByRole("row").filter({ hasText: personEmail });
  await expect(personRow).toContainText(personName);
  await expect(personRow).toContainText(roleName);
  await expect(personRow).toContainText("Active");

  await page.setViewportSize({ width: 390, height: 844 });
  await expectMobileManagementSurface(page, "People", "Create person");

  await personRow.getByRole("button", { name: "Disable", exact: true }).click();
  await page.getByRole("button", { name: "Disable account" }).click();
  await expect(personRow).toContainText("Disabled");

  await personRow.getByRole("button", { name: "Enable", exact: true }).click();
  await page.getByRole("button", { name: "Enable account" }).click();
  await expect(personRow).toContainText("Active");

  await personRow.getByRole("button", { name: "Delete", exact: true }).click();
  await page.getByRole("button", { name: "Delete account" }).click();
  await expect(personRow).toHaveCount(0);

  await page.goto("/roles");
  await expectMobileManagementSurface(page, "Access", "Create role");
  const unassignedRoleRow = page.getByRole("row").filter({ hasText: roleName });
  await expect(unassignedRoleRow).toContainText("0 people");
  await expect(
    unassignedRoleRow.getByRole("button", { name: "Delete", exact: true }),
  ).toBeEnabled();
  await unassignedRoleRow
    .getByRole("button", { name: "Delete", exact: true })
    .click();
  await page.getByRole("button", { name: "Delete role" }).click();
  await expect(unassignedRoleRow).toHaveCount(0);

  await page.setViewportSize({ width: 1280, height: 720 });
}

async function expectMobileManagementSurface(
  page: Page,
  heading: string,
  action: string,
) {
  await expect(
    page.getByRole("heading", { name: heading, exact: true }),
  ).toBeVisible();
  const actionButton = page.getByRole("button", { name: action, exact: true });
  await expect(actionButton).toBeVisible();
  await expect(actionButton).toBeEnabled();

  const overflow = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }));
  expect(overflow.scrollWidth).toBeLessThanOrEqual(overflow.clientWidth);

  const box = await actionButton.boundingBox();
  expect(box).not.toBeNull();
  if (!box) throw new Error(`${action} does not have a rendered box`);
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(
    page.viewportSize()?.width ?? 390,
  );
}
