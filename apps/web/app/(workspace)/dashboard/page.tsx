"use client";

import { useQuery } from "@tanstack/react-query";
import { Activity, ArrowUpRight, Boxes, Users } from "lucide-react";
import Link from "next/link";
import { PageHeader } from "@/components/page-header";
import { api } from "@/lib/api";
import "./dashboard.css";
import "@/components/page-header.css";

type Summary = {
  products: number;
  users: number;
  eventsLast24Hours: number;
  generatedAt: string;
};

export default function DashboardPage() {
  const summary = useQuery({
    queryKey: ["dashboard-summary"],
    queryFn: () => api<Summary>("/dashboard/summary"),
  });

  const metrics = [
    {
      label: "Products",
      value: summary.data?.products,
      detail: "Catalog records",
      icon: Boxes,
    },
    {
      label: "People",
      value: summary.data?.users,
      detail: "Active operators",
      icon: Users,
    },
    {
      label: "Events today",
      value: summary.data?.eventsLast24Hours,
      detail: "Audited actions",
      icon: Activity,
    },
  ];

  return (
    <>
      <PageHeader
        description="A compact pulse of the resources, people, and audited work moving through this instance."
        eyebrow="Friday brief"
        title="Good operations begin with clear signals."
      />
      <section className="metric-grid" aria-label="Workspace summary">
        {metrics.map(({ label, value, detail, icon: Icon }, index) => (
          <article
            className="metric"
            key={label}
            style={{ "--i": index } as React.CSSProperties}
          >
            <Icon aria-hidden size={20} strokeWidth={1.7} />
            <p>{label}</p>
            <strong>
              {summary.isPending ? "—" : (value ?? 0).toLocaleString()}
            </strong>
            <span>{detail}</span>
          </article>
        ))}
      </section>
      <section className="dashboard-notes">
        <div>
          <p className="eyebrow">Framework contract</p>
          <h2>One resource, end to end.</h2>
          <p>
            Products are the reference slice for migrations, typed HTTP
            operations, permissions, audit entries, and responsive management
            UI.
          </p>
        </div>
        <Link className="button secondary" href="/products">
          Open products
          <ArrowUpRight aria-hidden size={17} />
        </Link>
      </section>
    </>
  );
}
