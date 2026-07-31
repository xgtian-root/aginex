"use client";

import { ArrowRight, Braces, CheckCircle2 } from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";
import { type FormEvent, Suspense, useState } from "react";
import { toast } from "sonner";
import { ApiError, login } from "@/lib/api";
import { safeLocalRedirect } from "@/lib/navigation";
import "./login.css";

export default function LoginPage() {
  return (
    <Suspense fallback={<LoginLoading />}>
      <LoginForm />
    </Suspense>
  );
}

function LoginForm() {
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
      toast.success("Signed in. Your workspace is ready.");
      router.replace(safeLocalRedirect(search.get("next")));
    } catch (caught) {
      setError(
        caught instanceof ApiError
          ? caught.problem.detail
          : "We could not reach the API. Check that the server is running.",
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <main className="login-page">
      <section className="login-story" aria-labelledby="welcome-title">
        <div className="login-brand">
          <span className="brand-mark">A</span>
          <strong>aginex</strong>
        </div>
        <div>
          <p className="eyebrow">Agent-ready administration</p>
          <h1 id="welcome-title">A clear control room for serious work.</h1>
          <p className="login-lead">
            Every screen maps to an API contract, a permission, and a repeatable
            Coding Agent workflow.
          </p>
        </div>
        <ul className="login-points">
          <li>
            <CheckCircle2 aria-hidden size={18} />
            Explicit resource:action permissions
          </li>
          <li>
            <Braces aria-hidden size={18} />
            OpenAPI-backed frontend contracts
          </li>
        </ul>
      </section>
      <section className="login-form-wrap">
        <form className="login-form panel" onSubmit={signIn}>
          <div>
            <p className="eyebrow">Welcome back</p>
            <h2>Sign in to Aginex</h2>
            <p className="muted">Use the administrator created during setup.</p>
          </div>
          <div className="field">
            <label htmlFor="email">Email address</label>
            <input
              autoComplete="email"
              className="input"
              id="email"
              name="email"
              placeholder="admin@example.com"
              required
              type="email"
            />
          </div>
          <div className="field">
            <label htmlFor="password">Password</label>
            <input
              autoComplete="current-password"
              className="input"
              id="password"
              minLength={12}
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
            {pending ? "Opening workspace…" : "Sign in"}
            {!pending && <ArrowRight aria-hidden size={17} />}
          </button>
          <p className="login-note">
            No public registration. Administrators are created with the Aginex
            CLI.
          </p>
        </form>
      </section>
    </main>
  );
}

function LoginLoading() {
  return (
    <main className="login-page">
      <section className="login-story" aria-hidden />
      <section className="login-form-wrap" aria-live="polite">
        <div className="login-form panel">
          <p>Preparing sign in…</p>
        </div>
      </section>
    </main>
  );
}
