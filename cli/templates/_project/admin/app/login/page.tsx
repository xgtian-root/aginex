"use client";

import { ArrowRight, Braces, CheckCircle2 } from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { type FormEvent, Suspense, useState } from "react";
import { toast } from "sonner";
import { LocaleSwitcher } from "@/components/locale-switcher";
import { login } from "@/lib/api";
import { safeLocalRedirect } from "@/lib/navigation";
import { localizeApiError } from "@/lib/problem-message";
import "./login.css";

export default function LoginPage() {
  return (
    <Suspense fallback={<LoginLoading />}>
      <LoginForm />
    </Suspense>
  );
}

function LoginForm() {
  const t = useTranslations("Login");
  const translate = useTranslations();
  const router = useRouter();
  const search = useSearchParams();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");

  async function signIn(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPending(true);
    setError("");
    const data = new FormData(event.currentTarget);
    try {
      await login({
        email: String(data.get("email")),
        password: String(data.get("password")),
      });
      toast.success(t("successToast"));
      router.replace(safeLocalRedirect(search.get("next")));
    } catch (caught) {
      setError(
        localizeApiError(caught, translate, t("networkError"), {
          AUTHENTICATION_REQUIRED: t("credentialsError"),
        }),
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <main className="login-page">
      <section className="login-story" aria-labelledby="welcome-title">
        <div className="login-brand-row">
          <div className="login-brand">
            <span className="brand-mark">A</span>
            <strong>aginex</strong>
          </div>
          <LocaleSwitcher />
        </div>
        <div>
          <p className="eyebrow">{t("story.eyebrow")}</p>
          <h1 id="welcome-title">{t("story.title")}</h1>
          <p className="login-lead">{t("story.description")}</p>
        </div>
        <ul className="login-points">
          <li>
            <CheckCircle2 aria-hidden size={18} />
            {t("story.permissionPoint")}
          </li>
          <li>
            <Braces aria-hidden size={18} />
            {t("story.contractPoint")}
          </li>
        </ul>
      </section>
      <section className="login-form-wrap">
        <form className="login-form panel" onSubmit={signIn}>
          <div>
            <p className="eyebrow">{t("form.eyebrow")}</p>
            <h2>{t("form.title")}</h2>
            <p className="muted">{t("form.description")}</p>
          </div>
          <div className="field">
            <label htmlFor="email">{t("form.emailLabel")}</label>
            <input
              autoComplete="email"
              className="input"
              id="email"
              name="email"
              placeholder={t("form.emailPlaceholder")}
              required
              type="email"
            />
          </div>
          <div className="field">
            <label htmlFor="password">{t("form.passwordLabel")}</label>
            <input
              autoComplete="current-password"
              className="input"
              id="password"
              name="password"
              required
              type="password"
            />
          </div>
          {error && (
            <p className="form-error" role="alert">
              {error}
            </p>
          )}
          <button
            className="button login-button"
            disabled={pending}
            type="submit"
          >
            {pending ? t("form.pending") : t("form.submit")}
            {!pending && <ArrowRight aria-hidden size={17} />}
          </button>
          <p className="login-note">{t("form.note")}</p>
        </form>
      </section>
    </main>
  );
}

function LoginLoading() {
  const t = useTranslations("Login");
  return (
    <main className="login-page">
      <section className="login-story" aria-hidden />
      <section className="login-form-wrap" aria-live="polite">
        <div className="login-form panel">
          <LocaleSwitcher />
          <p>{t("loading")}</p>
        </div>
      </section>
    </main>
  );
}
