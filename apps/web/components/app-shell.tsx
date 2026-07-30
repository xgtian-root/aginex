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
import { useEffect } from "react";
import { api, type User } from "@/lib/api";

const navigation = [
  {
    href: "/dashboard",
    label: "Overview",
    icon: Gauge,
    permission: "dashboard:read",
  },
  {
    href: "/products",
    label: "Products",
    icon: Boxes,
    permission: "products:read",
  },
  { href: "/users", label: "People", icon: Users, permission: "users:read" },
  {
    href: "/roles",
    label: "Access",
    icon: ShieldCheck,
    permission: "roles:read",
  },
  {
    href: "/audit",
    label: "Audit trail",
    icon: FileClock,
    permission: "audit:read",
  },
  {
    href: "/files",
    label: "Files",
    icon: Images,
    permission: "files:read",
  },
];

export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const me = useQuery({
    queryKey: ["me"],
    queryFn: () => api<User>("/auth/me"),
    retry: false,
  });

  useEffect(() => {
    if (me.isError) {
      router.replace(`/login?next=${encodeURIComponent(pathname)}`);
    }
  }, [me.isError, pathname, router]);

  if (me.isPending) {
    return <ShellSkeleton />;
  }
  if (!me.data) {
    return null;
  }

  const allowed = navigation.filter((item) =>
    me.data.permissions.includes(item.permission),
  );
  const active = allowed.find((item) => pathname.startsWith(item.href));

  async function signOut() {
    await api<void>("/auth/logout", { method: "POST" });
    router.replace("/login");
  }

  return (
    <div className="shell">
      <a className="skip-link" href="#main-content">
        Skip to main content
      </a>
      <aside className="rail">
        <Link className="brand" href="/dashboard" aria-label="Aginex home">
          <span className="brand-mark">A</span>
          <span>
            <strong>aginex</strong>
            <small>control room</small>
          </span>
        </Link>
        <nav aria-label="Primary navigation">
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
                <span>{item.label}</span>
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
            aria-label="Sign out"
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
          <span className="eyebrow">{active?.label ?? "Workspace"}</span>
          <span className="environment">
            <i aria-hidden />
            Development
          </span>
        </header>
        <main id="main-content">{children}</main>
      </div>
    </div>
  );
}

function ShellSkeleton() {
  return (
    <div className="loading-shell" aria-live="polite">
      <span className="brand-mark">A</span>
      <p>Opening your workspace…</p>
    </div>
  );
}
