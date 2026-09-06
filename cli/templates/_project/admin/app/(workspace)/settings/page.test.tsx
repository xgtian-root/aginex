// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import messages from "../../../messages/en.json";
import SettingsPage from "./page";

const mocks = vi.hoisted(() => ({
  getCurrentUser: vi.fn(),
  getStorageSettings: vi.fn(),
  listStorageProfiles: vi.fn(),
  updateFileUploadPolicy: vi.fn(),
}));

vi.mock("@/lib/api", () => ({
  activateStorageProfile: vi.fn(),
  archiveStorageProfile: vi.fn(),
  createStorageProfile: vi.fn(),
  deleteStorageProfile: vi.fn(),
  getCurrentUser: mocks.getCurrentUser,
  getStorageSettings: mocks.getStorageSettings,
  listStorageProfiles: mocks.listStorageProfiles,
  restoreStorageProfile: vi.fn(),
  testStorageProfile: vi.fn(),
  updateFileUploadPolicy: mocks.updateFileUploadPolicy,
  updateStorageProfile: vi.fn(),
}));

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

beforeEach(() => {
  vi.clearAllMocks();
  mocks.getCurrentUser.mockResolvedValue({
    grants: [
      { permission: "storage-profiles:read", scope: "all" },
      { permission: "storage-profiles:update", scope: "all" },
      { permission: "storage-profiles:create", scope: "all" },
    ],
  });
  mocks.getStorageSettings.mockResolvedValue({
    etag: '"settings-4"',
    value: {
      runtimeActiveProfileId: "profile-local",
      pendingActiveProfileId: "profile-local",
      environmentManaged: true,
      restartRequired: false,
      revision: 4,
      runtimeRevision: 4,
      unboundFileCount: 0,
      runtimeFileUploadPolicy: {
        maxUploadBytes: 64 * 1024 * 1024,
        resumableUploadsEnabled: false,
      },
      pendingFileUploadPolicy: {
        maxUploadBytes: 64 * 1024 * 1024,
        resumableUploadsEnabled: false,
      },
    },
  });
  mocks.listStorageProfiles.mockResolvedValue({
    etag: '"profiles-4"',
    value: { items: [], page: 1, pageSize: 20, total: 0 },
  });
});

afterEach(cleanup);

describe("SettingsPage file upload policy", () => {
  it("keeps file policy editable while environment-managed profiles stay read only", async () => {
    renderSettingsPage();

    expect(
      await screen.findByRole("heading", { name: "File upload policy" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("spinbutton", {
        name: /^Maximum single-file size/,
      }),
    ).toBeEnabled();
    expect(
      screen.getByRole("checkbox", { name: /^Enable resumable uploads/ }),
    ).toBeEnabled();
    expect(
      screen.queryByRole("button", { name: "New profile" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("Managed by environment")).toBeInTheDocument();
  });
});

describe("SettingsPage OSS profile editor", () => {
  it("requires a browser-visible OSS access origin", async () => {
    mocks.getStorageSettings.mockResolvedValue({
      ...(await mocks.getStorageSettings()),
      value: {
        ...(await mocks.getStorageSettings()).value,
        environmentManaged: false,
      },
    });

    renderSettingsPage();
    fireEvent.click(await screen.findByRole("button", { name: "New profile" }));

    const input = screen.getByRole("textbox", {
      name: /^External access domain/,
    });
    expect(input).toBeRequired();
    expect(input).toHaveAttribute("type", "url");
    expect(input).toHaveAttribute(
      "placeholder",
      "https://files.example.com",
    );
  });
});

function renderSettingsPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <NextIntlClientProvider locale="en" messages={messages} timeZone="UTC">
      <QueryClientProvider client={queryClient}>
        <SettingsPage />
      </QueryClientProvider>
    </NextIntlClientProvider>,
  );
}
