"use client";

import { useMutation } from "@tanstack/react-query";
import { ArrowRight, Braces, CheckCircle2 } from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import {
  type FormEvent,
  Suspense,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import { toast } from "sonner";
import { LocaleSwitcher } from "@/components/locale-switcher";
import { createCaptcha, login } from "@/lib/api";
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

  const [captchaCode, setCaptchaCode] = useState("");
  const [loadedImage, setLoadedImage] = useState("");
  const [failedImage, setFailedImage] = useState("");
  const submitting = useRef(false);
  const captcha = useMutation({ mutationFn: createCaptcha, retry: false });
  const { mutate } = captcha;
  const refreshCaptcha = useCallback(() => {
    setCaptchaCode("");
    setLoadedImage("");
    setFailedImage("");
    mutate();
  }, [mutate]);

  useEffect(() => {
    refreshCaptcha();
  }, [refreshCaptcha]);
  useEffect(() => {
    if (!captcha.data || !captcha.isSuccess || pending) return;
    const timer = window.setTimeout(
      refreshCaptcha,
      Math.max(0, Date.parse(captcha.data.expiresAt) - Date.now()),
    );
    return () => window.clearTimeout(timer);
  }, [captcha.data, captcha.isSuccess, pending, refreshCaptcha]);

  const imageFailed =
    captcha.isSuccess && failedImage === captcha.data.captchaId;
  const captchaReady =
    captcha.isSuccess && loadedImage === captcha.data.captchaId && !imageFailed;

  async function signIn(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (submitting.current || !captchaReady || !captchaCode.trim()) return;
    if (Date.parse(captcha.data.expiresAt) <= Date.now()) {
      refreshCaptcha();
      return;
    }
    submitting.current = true;
    setPending(true);
    setError("");
    const data = new FormData(event.currentTarget);
    try {
      await login({
        captchaId: captcha.data.captchaId,
        captchaCode,
        email: String(data.get("email")),
        password: String(data.get("password")),
      });
      toast.success(t("successToast"));
      router.replace(safeLocalRedirect(search.get("next")));
    } catch (caught) {
      refreshCaptcha();
      setError(
        localizeApiError(caught, translate, t("networkError"), {
          AUTHENTICATION_REQUIRED: t("credentialsError"),
        }),
      );
    } finally {
      submitting.current = false;
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
          <div className="field">
            <label htmlFor="captchaCode">{t("captcha.label")}</label>
            <input
              autoComplete="off"
              className="input"
              id="captchaCode"
              name="captchaCode"
              required
              maxLength={4}
              spellCheck={false}
              value={captchaCode}
              onChange={(event) => setCaptchaCode(event.target.value)}
              aria-describedby="captcha-help"
            />
            <div className="login-captcha-row" aria-busy={captcha.isPending}>
              {captcha.isSuccess && !imageFailed && (
                // A fresh server-generated PNG must be displayed unchanged.
                // biome-ignore lint/performance/noImgElement: dynamic captcha Data URL
                <img
                  key={captcha.data.captchaId}
                  src={captcha.data.image}
                  alt={t("captcha.imageAlt")}
                  width={160}
                  height={56}
                  onLoad={() => setLoadedImage(captcha.data.captchaId)}
                  onError={() => setFailedImage(captcha.data.captchaId)}
                />
              )}
              {captcha.isPending && (
                <span role="status">{t("captcha.loading")}</span>
              )}
              <button
                className="button secondary"
                type="button"
                disabled={pending || captcha.isPending}
                onClick={refreshCaptcha}
              >
                {captcha.isError || imageFailed
                  ? t("captcha.retry")
                  : t("captcha.refresh")}
              </button>
            </div>
            <p className="muted" id="captcha-help">
              {t("captcha.hint")}
            </p>
            {(captcha.isError || imageFailed) && (
              <p className="form-error" role="alert">
                {captcha.isError
                  ? localizeApiError(
                      captcha.error,
                      translate,
                      t("captcha.loadError"),
                    )
                  : t("captcha.loadError")}
              </p>
            )}
          </div>
          {error && (
            <p className="form-error" role="alert">
              {error}
            </p>
          )}
          <button
            className="button login-button"
            disabled={pending || !captchaReady}
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
