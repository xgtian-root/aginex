// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import messages from "../messages/en.json";
import { SetupWizard } from "./setup-wizard";

const apiMocks = vi.hoisted(() => ({
  completeSetup: vi.fn(),
  getSetupStatus: vi.fn(),
  getSystemMode: vi.fn(),
  testSetupDatabase: vi.fn(),
}));

vi.mock("@/lib/api", () => ({
  ApiError: class ApiError extends Error {
    problem = { code: "REQUEST_FAILED" };
    response = new Response(null, { status: 500 });
  },
  completeSetup: apiMocks.completeSetup,
  getSetupStatus: apiMocks.getSetupStatus,
  getSystemMode: apiMocks.getSystemMode,
  testSetupDatabase: apiMocks.testSetupDatabase,
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ refresh: vi.fn() }),
}));

beforeEach(() => {
  vi.clearAllMocks();
  apiMocks.getSystemMode.mockResolvedValue({ mode: "setup" });
  apiMocks.getSetupStatus.mockResolvedValue({
    stage: "waiting",
    status: "required",
  });
  apiMocks.testSetupDatabase.mockResolvedValue({ status: "ok" });
  apiMocks.completeSetup.mockResolvedValue({ status: "initializing" });
});

afterEach(cleanup);

describe("SetupWizard database configuration", () => {
  it("starts with separate SQLite directory and file-name fields", async () => {
    renderWizard();
    await databaseStepReady();

    expect(screen.getByLabelText("Database directory")).toHaveValue("data");
    expect(screen.getByLabelText("Database file name")).toHaveValue(
      "aginex.db",
    );
    expect(screen.queryByLabelText("Host")).not.toBeInTheDocument();
    expect(screen.queryByText("Connection string")).not.toBeInTheDocument();

    submitDatabaseForm();

    await waitFor(() =>
      expect(apiMocks.testSetupDatabase).toHaveBeenCalledOnce(),
    );
    expect(apiMocks.testSetupDatabase.mock.calls[0]?.[0]).toEqual({
      database: {
        driver: "sqlite",
        sqlite: { directory: "data", filename: "aginex.db" },
      },
    });
  });

  it("shows driver-specific server defaults and security modes", async () => {
    renderWizard();
    await databaseStepReady();

    fireEvent.click(screen.getByLabelText("PostgreSQL"));

    expect(screen.getByLabelText("Host")).toHaveValue("localhost");
    expect(screen.getByLabelText("Port")).toHaveValue(5432);
    expect(screen.getByLabelText("Database name")).toHaveValue("aginex");
    expect(screen.getByLabelText("Username")).toHaveValue("aginex");
    expect(screen.getByLabelText("Database password")).toHaveValue("");
    expect(screen.getByLabelText("Database password")).toBeRequired();
    expect(screen.getByLabelText("SSL mode")).toHaveValue("require");
    expect(screen.queryByLabelText("TLS mode")).not.toBeInTheDocument();
    submitDatabaseForm();
    expect(apiMocks.testSetupDatabase).not.toHaveBeenCalled();

    fireEvent.click(screen.getByLabelText("MySQL"));

    expect(screen.getByLabelText("Port")).toHaveValue(3306);
    expect(screen.getByLabelText("TLS mode")).toHaveValue("disabled");
    expect(screen.queryByLabelText("SSL mode")).not.toBeInTheDocument();
  });

  it("submits only the selected nested object and preserves password whitespace", async () => {
    renderWizard();
    await databaseStepReady();
    fireEvent.click(screen.getByLabelText("PostgreSQL"));

    fireEvent.change(screen.getByLabelText("Host"), {
      target: { value: " db.example.test " },
    });
    fireEvent.change(screen.getByLabelText("Port"), {
      target: { value: "5544" },
    });
    fireEvent.change(screen.getByLabelText("Database name"), {
      target: { value: " workspace " },
    });
    fireEvent.change(screen.getByLabelText("Username"), {
      target: { value: " owner " },
    });
    fireEvent.change(screen.getByLabelText("Database password"), {
      target: { value: " keep surrounding spaces " },
    });
    fireEvent.change(screen.getByLabelText("SSL mode"), {
      target: { value: "verify-full" },
    });

    submitDatabaseForm();

    await waitFor(() =>
      expect(apiMocks.testSetupDatabase).toHaveBeenCalledOnce(),
    );
    const request = apiMocks.testSetupDatabase.mock.calls[0]?.[0];
    expect(request).toEqual({
      database: {
        driver: "postgres",
        postgres: {
          host: "db.example.test",
          port: 5544,
          database: "workspace",
          username: "owner",
          password: " keep surrounding spaces ",
          sslMode: "verify-full",
        },
      },
    });
    expect(request.database).not.toHaveProperty("dsn");
    expect(request.database).not.toHaveProperty("sqlite");
    expect(request.database).not.toHaveProperty("mysql");
    expect(screen.getByLabelText("Host")).toHaveValue("db.example.test");
    expect(screen.getByLabelText("Database password")).toHaveValue(
      " keep surrounding spaces ",
    );
  });

  it("revokes verification after any field changes", async () => {
    renderWizard();
    await databaseStepReady();
    fireEvent.click(screen.getByLabelText("PostgreSQL"));
    fireEvent.change(screen.getByLabelText("Database password"), {
      target: { value: "verified database password" },
    });
    submitDatabaseForm();

    expect(await screen.findByText("Connection verified")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeEnabled();

    fireEvent.change(screen.getByLabelText("Host"), {
      target: { value: "database.internal" },
    });

    expect(screen.queryByText("Connection verified")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
  });

  it("reveals only the database password and resets visibility by driver", async () => {
    renderWizard();
    await databaseStepReady();
    fireEvent.click(screen.getByLabelText("PostgreSQL"));

    const password = screen.getByLabelText("Database password");
    expect(password).toHaveAttribute("type", "password");

    fireEvent.click(
      screen.getByRole("button", { name: "Show database password" }),
    );
    expect(password).toHaveAttribute("type", "text");

    fireEvent.click(screen.getByLabelText("MySQL"));
    expect(screen.getByLabelText("Database password")).toHaveAttribute(
      "type",
      "password",
    );
  });
});

describe("SetupWizard administrator password", () => {
  it("accepts a single-character password", async () => {
    await renderAdministratorStep();

    const password = "x";
    const passwordInput = screen.getByLabelText("Password");
    const confirmationInput = screen.getByLabelText("Confirm password");

    expect(passwordInput).toBeRequired();
    expect(confirmationInput).toBeRequired();
    expect(passwordInput).not.toHaveAttribute("minlength");
    expect(passwordInput).not.toHaveAttribute("maxlength");
    expect(confirmationInput).not.toHaveAttribute("minlength");
    expect(confirmationInput).not.toHaveAttribute("maxlength");
    expect(screen.queryByText("12+ characters")).not.toBeInTheDocument();

    submitAdministratorForm(password);

    await waitFor(() => expect(apiMocks.completeSetup).toHaveBeenCalledOnce());
    const submittedPassword =
      apiMocks.completeSetup.mock.calls[0]?.[0].administrator.password;
    expect(submittedPassword === password).toBe(true);
  });

  it("accepts a password longer than the previous maximum", async () => {
    await renderAdministratorStep();

    const password = "p".repeat(1_025);
    submitAdministratorForm(password);

    await waitFor(() => expect(apiMocks.completeSetup).toHaveBeenCalledOnce());
    const submittedPassword =
      apiMocks.completeSetup.mock.calls[0]?.[0].administrator.password;
    expect(submittedPassword?.length).toBe(password.length);
    expect(submittedPassword === password).toBe(true);
  });
});

async function databaseStepReady() {
  await screen.findByRole("heading", {
    name: "Locate the system of record.",
  });
}

function submitDatabaseForm() {
  const button = screen.getByRole("button", { name: /Test connection/ });
  fireEvent.click(button);
}

async function renderAdministratorStep() {
  renderWizard();
  await databaseStepReady();
  submitDatabaseForm();
  await screen.findByText("Connection verified");
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  await screen.findByRole("heading", { name: "Name the first operator." });
}

function submitAdministratorForm(password: string) {
  fireEvent.change(screen.getByLabelText("Email address"), {
    target: { value: "admin@example.com" },
  });
  fireEvent.change(screen.getByLabelText("Password"), {
    target: { value: password },
  });
  fireEvent.change(screen.getByLabelText("Confirm password"), {
    target: { value: password },
  });
  fireEvent.click(screen.getByRole("button", { name: "Initialize workspace" }));
}

function renderWizard() {
  const queryClient = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false },
    },
  });
  return render(
    <NextIntlClientProvider locale="en" messages={messages} timeZone="UTC">
      <QueryClientProvider client={queryClient}>
        <SetupWizard />
      </QueryClientProvider>
    </NextIntlClientProvider>,
  );
}
