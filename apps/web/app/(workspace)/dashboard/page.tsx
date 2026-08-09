"use client";

import { useQuery } from "@tanstack/react-query";
import { Activity, ArrowUpRight, Boxes, Users } from "lucide-react";
import Link from "next/link";
import { useFormatter, useTranslations } from "next-intl";
import { PageHeader } from "@/components/page-header";
import { getDashboardSummary } from "@/lib/api";
import "./dashboard.css";
import "@/components/page-header.css";

export default function DashboardPage() {
  const t = useTranslations("Dashboard");
  const common = useTranslations("Common");
  const format = useFormatter();
  const summary = useQuery({
    queryKey: ["dashboard-summary"],
    queryFn: getDashboardSummary,
  });

  const metrics = [
    {
      key: "products",
      label: t("metrics.products.label"),
      value: summary.data?.products,
      detail: t("metrics.products.detail"),
      icon: Boxes,
    },
    {
      key: "people",
      label: t("metrics.people.label"),
      value: summary.data?.users,
      detail: t("metrics.people.detail"),
      icon: Users,
    },
    {
      key: "eventsToday",
      label: t("metrics.eventsToday.label"),
      value: summary.data?.eventsLast24Hours,
      detail: t("metrics.eventsToday.detail"),
      icon: Activity,
    },
  ];

  return (
    <>
      <PageHeader
        description={t("header.description")}
        eyebrow={t("header.eyebrow")}
        title={t("header.title")}
      />
      <section className="metric-grid" aria-label={t("summaryAriaLabel")}>
        {metrics.map(({ key, label, value, detail, icon: Icon }, index) => (
          <article
            className="metric"
            key={key}
            style={{ "--i": index } as React.CSSProperties}
          >
            <Icon aria-hidden size={20} strokeWidth={1.7} />
            <p>{label}</p>
            <strong>
              {summary.isPending
                ? common("notAvailable")
                : format.number(value ?? 0)}
            </strong>
            <span>{detail}</span>
          </article>
        ))}
      </section>
      <section className="dashboard-notes">
        <div>
          <p className="eyebrow">{t("contract.eyebrow")}</p>
          <h2>{t("contract.title")}</h2>
          <p>{t("contract.description")}</p>
        </div>
        <Link className="button secondary" href="/products">
          {t("contract.action")}
          <ArrowUpRight aria-hidden size={17} />
        </Link>
      </section>
    </>
  );
}
