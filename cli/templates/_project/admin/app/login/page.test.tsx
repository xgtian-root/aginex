// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import messages from "../../messages/en.json";
import LoginPage from "./page";

const mocks = vi.hoisted(() => ({
  login: vi.fn(),
  replace: vi.fn(),
  toastSuccess: vi.fn(),
}));

vi.mock("@/lib/api", () => ({
  ApiError: class ApiError extends Error {
    problem = { code: "REQUEST_FAILED" };
  },
  login: mocks.login,
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ refresh: vi.fn(), replace: mocks.replace }),
  useSearchParams: () => new URLSearchParams(),
}));

vi.mock("sonner", () => ({
  toast: { success: mocks.toastSuccess },
}));

beforeEach(() => {
  vi.clearAllMocks();
  mocks.login.mockResolvedValue(undefined);
});

afterEach(cleanup);

describe("LoginPage administrator password", () => {
  it("submits a required single-character password", async () => {
    renderLoginPage();

    const password = "x";
    const passwordInput = await screen.findByLabelText("Password");
    expect(passwordInput).toBeRequired();
    expect(passwordInput).not.toHaveAttribute("minlength");
    expect(passwordInput).not.toHaveAttribute("maxlength");

    submitLogin(password);

    await waitFor(() => expect(mocks.login).toHaveBeenCalledOnce());
    const submittedPassword = mocks.login.mock.calls[0]?.[0].password;
    expect(submittedPassword === password).toBe(true);
  });

  it("submits a password longer than the previous setup maximum", async () => {
    renderLoginPage();
    await screen.findByLabelText("Password");

    const password = "p".repeat(1_025);
    submitLogin(password);

    await waitFor(() => expect(mocks.login).toHaveBeenCalledOnce());
    const submittedPassword = mocks.login.mock.calls[0]?.[0].password;
    expect(submittedPassword?.length).toBe(password.length);
    expect(submittedPassword === password).toBe(true);
  });
});

function renderLoginPage() {
  return render(
    <NextIntlClientProvider locale="en" messages={messages} timeZone="UTC">
      <LoginPage />
    </NextIntlClientProvider>,
  );
}

function submitLogin(password: string) {
  fireEvent.change(screen.getByLabelText("Email address"), {
    target: { value: "admin@example.com" },
  });
  fireEvent.change(screen.getByLabelText("Password"), {
    target: { value: password },
  });
  fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
}
