// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import messages from "../messages/en.json";
import { LocaleSwitcher, serializeLocaleCookie } from "./locale-switcher";

const refresh = vi.fn();

vi.mock("next/navigation", () => ({
  useRouter: () => ({ refresh }),
}));

beforeEach(() => {
  refresh.mockReset();
  document.documentElement.lang = "en";
  document.documentElement.dir = "ltr";
});

afterEach(cleanup);

describe("LocaleSwitcher", () => {
  it("renders every supported locale", () => {
    renderSwitcher();

    expect(screen.getByRole("option", { name: "English" })).toBeInTheDocument();
    expect(
      screen.getByRole("option", { name: "简体中文" }),
    ).toBeInTheDocument();
  });

  it("persists an allowlisted selection and refreshes server components", () => {
    renderSwitcher();

    fireEvent.change(screen.getByRole("combobox"), {
      target: { value: "zh-CN" },
    });

    expect(document.cookie).toContain("aginex_locale=zh-CN");
    expect(document.documentElement.lang).toBe("zh-CN");
    expect(document.documentElement.dir).toBe("ltr");
    expect(refresh).toHaveBeenCalledOnce();
  });
});

describe("serializeLocaleCookie", () => {
  it("adds Secure only on HTTPS", () => {
    expect(serializeLocaleCookie("en", "https:")).toContain("; Secure");
    expect(serializeLocaleCookie("en", "http:")).not.toContain("; Secure");
  });
});

function renderSwitcher() {
  return render(
    <NextIntlClientProvider locale="en" messages={messages} timeZone="UTC">
      <LocaleSwitcher />
    </NextIntlClientProvider>,
  );
}
