// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import messages from "../../messages/en.json";
import LoginPage from "./page";

const mocks = vi.hoisted(() => ({
  login: vi.fn(),
  createCaptcha: vi.fn(),
  replace: vi.fn(),
  toastSuccess: vi.fn(),
}));

vi.mock("@/lib/api", () => ({
  ApiError: class ApiError extends Error {
    problem: { code: string };
    constructor(problem: { code: string }) {
      super(problem.code);
      this.problem = problem;
    }
  },
  login: mocks.login,
  createCaptcha: mocks.createCaptcha,
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
  mocks.createCaptcha.mockReset();
  mocks.createCaptcha.mockImplementation(async () => challenge());
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

    await loadImage();
    submitLogin(password);

    await waitFor(() => expect(mocks.login).toHaveBeenCalledOnce());
    const submittedPassword = mocks.login.mock.calls[0]?.[0].password;
    expect(submittedPassword === password).toBe(true);
  });

  it("submits a password longer than the previous setup maximum", async () => {
    renderLoginPage();
    await screen.findByLabelText("Password");

    const password = "p".repeat(1_025);
    await loadImage();
    submitLogin(password);

    await waitFor(() => expect(mocks.login).toHaveBeenCalledOnce());
    const submittedPassword = mocks.login.mock.calls[0]?.[0].password;
    expect(submittedPassword?.length).toBe(password.length);
    expect(submittedPassword === password).toBe(true);
  });
});

function renderLoginPage(strict = false) {
  const content = (
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { mutations: { retry: false } } })
      }
    >
      <NextIntlClientProvider locale="en" messages={messages} timeZone="UTC">
        <LoginPage />
      </NextIntlClientProvider>
    </QueryClientProvider>
  );
  return render(strict ? <StrictMode>{content}</StrictMode> : content);
}

function submitLogin(password: string) {
  fireEvent.change(screen.getByLabelText("Email address"), {
    target: { value: "admin@example.com" },
  });
  fireEvent.change(screen.getByLabelText("Password"), {
    target: { value: password },
  });
  fireEvent.change(screen.getByLabelText("Verification code"), {
    target: { value: "a2b3" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
}

let sequence = 0;
function challenge() {
  sequence += 1;
  return {
    captchaId: `captcha-${sequence}`,
    image: "data:image/png;base64,test",
    expiresAt: new Date(Date.now() + 300_000).toISOString(),
  };
}
async function loadImage() {
  const image = await screen.findByAltText("Login verification image");
  fireEvent.load(image);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Sign in" })).toBeEnabled(),
  );
  return image;
}

describe("Login captcha", () => {
  it("requires the image to load and submits the challenge with credentials", async () => {
    renderLoginPage();
    expect(screen.getByRole("button", { name: "Sign in" })).toBeDisabled();
    await loadImage();
    submitLogin("secret");
    await waitFor(() => expect(mocks.login).toHaveBeenCalledOnce());
    expect(mocks.login.mock.calls[0][0]).toMatchObject({
      captchaId: expect.stringMatching(/^captcha-/),
      captchaCode: "a2b3",
    });
    await waitFor(() => expect(mocks.replace).toHaveBeenCalled());
  });

  it("refreshes and clears only the verification code", async () => {
    renderLoginPage();
    await loadImage();
    fireEvent.change(screen.getByLabelText("Verification code"), {
      target: { value: "ABCD" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "secret" },
    });
    fireEvent.click(screen.getByRole("button", { name: "New image" }));
    await waitFor(() => expect(mocks.createCaptcha).toHaveBeenCalledTimes(2));
    expect(screen.getByLabelText("Verification code")).toHaveValue("");
    expect(screen.getByLabelText("Password")).toHaveValue("secret");
    expect(screen.getByRole("button", { name: "Sign in" })).toBeDisabled();
  });

  it("offers retry after request or image loading failure", async () => {
    mocks.createCaptcha.mockRejectedValueOnce(new Error("offline"));
    renderLoginPage();
    fireEvent.click(
      await screen.findByRole("button", { name: "Reload image" }),
    );
    const image = await screen.findByAltText("Login verification image");
    fireEvent.error(image);
    expect(
      await screen.findByRole("button", { name: "Reload image" }),
    ).toBeEnabled();
    expect(screen.getByRole("button", { name: "Sign in" })).toBeDisabled();
    expect(mocks.createCaptcha).toHaveBeenCalledTimes(2);
  });

  it("fetches a new challenge after a rejected login while preserving credentials", async () => {
    mocks.login.mockRejectedValueOnce(
      new ApiError(
        { code: "CAPTCHA_INVALID" } as never,
        new Response(null, { status: 400 }),
      ),
    );
    renderLoginPage();
    await loadImage();
    submitLogin("secret");
    await waitFor(() => expect(mocks.createCaptcha).toHaveBeenCalledTimes(2));
    expect(screen.getByLabelText("Verification code")).toHaveValue("");
    expect(screen.getByLabelText("Password")).toHaveValue("secret");
    expect(screen.getByLabelText("Email address")).toHaveValue(
      "admin@example.com",
    );
    expect(mocks.replace).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent(
      "The verification code is incorrect or expired",
    );
  });

  it("rejects duplicate submissions while login is in flight", async () => {
    let finish!: () => void;
    mocks.login.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    renderLoginPage();
    await loadImage();
    submitLogin("secret");
    const form = screen.getByLabelText("Password").closest("form");
    if (!form) throw new Error("Missing login form");
    fireEvent.submit(form);
    fireEvent.submit(form);
    expect(mocks.login).toHaveBeenCalledOnce();
    await act(async () => finish());
  });

  it("replaces an expired challenge", async () => {
    mocks.createCaptcha.mockResolvedValueOnce({
      ...challenge(),
      expiresAt: new Date(Date.now() - 1).toISOString(),
    });
    renderLoginPage();
    await waitFor(() => expect(mocks.createCaptcha).toHaveBeenCalledTimes(2));
    await loadImage();
  });

  it("ignores a stale initial response when Strict Mode starts another request", async () => {
    const resolvers: ((value: ReturnType<typeof challenge>) => void)[] = [];
    mocks.createCaptcha.mockImplementation(
      () => new Promise((resolve) => resolvers.push(resolve)),
    );
    renderLoginPage(true);
    await waitFor(() => expect(resolvers).toHaveLength(2));
    const current = challenge();
    await act(async () => resolvers[1](current));
    await loadImage();
    await act(async () => resolvers[0](challenge()));
    submitLogin("secret");
    await waitFor(() => expect(mocks.login).toHaveBeenCalledOnce());
    expect(mocks.login.mock.calls[0][0].captchaId).toBe(current.captchaId);
  });
});
