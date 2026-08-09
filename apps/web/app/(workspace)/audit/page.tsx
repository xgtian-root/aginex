"use client";

import { useFormatter, useTranslations } from "next-intl";
import { ResourceList } from "@/components/resource-list";
import { listAuditLogs } from "@/lib/api";
import "../products/products.css";

export default function AuditPage() {
  const t = useTranslations("Audit");
  const format = useFormatter();

  return (
    <ResourceList
      columns={[
        { key: "action", label: t("columns.action") },
        { key: "resource", label: t("columns.resource") },
        { key: "summary", label: t("columns.summary") },
        { key: "ipAddress", label: t("columns.ipAddress") },
        {
          key: "createdAt",
          label: t("columns.time"),
          format: (value) =>
            format.dateTime(new Date(String(value)), {
              year: "numeric",
              month: "short",
              day: "numeric",
              hour: "2-digit",
              minute: "2-digit",
              second: "2-digit",
            }),
        },
      ]}
      description={t("header.description")}
      eyebrow={t("header.eyebrow")}
      load={listAuditLogs}
      queryKey="audit-logs"
      resourceLabel={t("resourceLabel")}
      title={t("header.title")}
    />
  );
}
