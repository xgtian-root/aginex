"use client";

import { useQuery } from "@tanstack/react-query";
import {
  Boxes,
  ChevronRight,
  CircleUserRound,
  FileClock,
  Gauge,
  Images,
  LogOut,
  ShieldCheck,
  Users,
} from "lucide-react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useEffect } from "react";
import { LocaleSwitcher } from "@/components/locale-switcher";
import { ApiError, getCurrentUser, logout } from "@/lib/api";

const navigation = [
  {
    href: "/dashboard",
    label: "overview",
    icon: Gauge,
    permission: "dashboard:read",
  },
  {
    href: "/products",
    label: "products",
    icon: Boxes,
    permission: "products:read",
  },
  { href: "/users", label: "people", icon: Users, permission: "users:read" },
  {
    href: "/roles",
    label: "access",
    icon: ShieldCheck,
    permission: "roles:read",
  },
  {
    href: "/audit",
    label: "auditTrail",
    icon: FileClock,
    permission: "audit:read",
  },
  {
    href: "/files",
    label: "files",
    icon: Images,
    permission: "files:read",
  },
] as const;

export function AppShell({ children }: { children: React.ReactNode }) {
  const t = useTranslations("Shell");
  const pathname = usePathname();
  const router = useRouter();
  const me = useQuery({
    queryKey: ["me"],
    queryFn: getCurrentUser,
    retry: false,
  });
  const needsLogin =
    me.error instanceof ApiError && me.error.response.status === 401;

  useEffect(() => {
    if (needsLogin) {
      router.replace(`/login?next=${encodeURIComponent(pathname)}`);
    }
  }, [needsLogin, pathname, router]);

  if (me.isPending) {
    return <ShellSkeleton />;
  }
  if (me.isError && !needsLogin) {
    return (
      <div className="shell-query-error" role="alert">
        <span className="brand-mark" aria-hidden>
          A
        </span>
        <LocaleSwitcher />
        <p className="eyebrow">{t("error.eyebrow")}</p>
        <h1>{t("error.title")}</h1>
        <p>{t("error.description")}</p>
        <button className="button" onClick={() => me.refetch()} type="button">
          {t("error.action")}
        </button>
      </div>
    );
  }
  if (!me.data) {
    return null;
  }

  const allowed = navigation.filter((item) =>
    me.data.permissions.includes(item.permission),
  );
  const active = allowed.find((item) => pathname.startsWith(item.href));

  async function signOut() {
    await logout();
    router.replace("/login");
  }

  return (
    <div className="shell">
      <a className="skip-link" href="#main-content">
        {t("skipLink")}
      </a>
      <aside className="rail">
        <Link
          className="brand"
          href="/dashboard"
          aria-label={t("homeAriaLabel")}
        >
          <span className="brand-mark">A</span>
          <span>
            <strong>aginex</strong>
            <small>{t("brandTagline")}</small>
          </span>
        </Link>
        <nav aria-label={t("primaryNavigationAriaLabel")}>
          {allowed.map((item) => {
            const Icon = item.icon;
            const isActive = pathname.startsWith(item.href);
            return (
              <Link
                className={isActive ? "nav-link active" : "nav-link"}
                href={item.href}
                key={item.href}
              >
                <Icon aria-hidden size={18} strokeWidth={1.8} />
                <span>{t(`navigation.${item.label}`)}</span>
                {isActive && <ChevronRight className="nav-caret" size={15} />}
              </Link>
            );
          })}
        </nav>
        <div className="operator">
          <CircleUserRound aria-hidden size={22} />
          <span>
            <strong>{me.data.displayName}</strong>
            <small>{me.data.email}</small>
          </span>
          <button
            aria-label={t("signOutAriaLabel")}
            className="icon-button"
            onClick={signOut}
            type="button"
          >
            <LogOut aria-hidden size={17} />
          </button>
        </div>
      </aside>
      <div className="workbench">
        <header className="topbar">
          <span className="eyebrow">
            {active ? t(`navigation.${active.label}`) : t("workspaceFallback")}
          </span>
          <div className="topbar-actions">
            <span className="environment">
              <i aria-hidden />
              {t("environment")}
            </span>
            <LocaleSwitcher />
          </div>
        </header>
        <main id="main-content">{children}</main>
      </div>
    </div>
  );
}

function ShellSkeleton() {
  const t = useTranslations("Shell");
  return (
    <div className="loading-shell" aria-live="polite">
      <span className="brand-mark">A</span>
      <p>{t("openingWorkspace")}</p>
      <LocaleSwitcher />
    </div>
  );
}
