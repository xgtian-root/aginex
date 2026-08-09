"use client";

import { useFormatter, useTranslations } from "next-intl";
import { ResourceList } from "@/components/resource-list";
import { listUsers } from "@/lib/api";
import "../products/products.css";

export default function UsersPage() {
  const t = useTranslations("Users");
  const common = useTranslations("Common");
  const format = useFormatter();

  return (
    <ResourceList
      columns={[
        { key: "displayName", label: t("columns.name") },
        { key: "email", label: t("columns.email") },
        {
          key: "status",
          label: t("columns.status"),
          format: (value) => (
            <span className="status">
              {isUserStatus(value)
                ? t(`statuses.${value}`)
                : common("notAvailable")}
            </span>
          ),
        },
        {
          key: "createdAt",
          label: t("columns.created"),
          format: (value) =>
            format.dateTime(new Date(String(value)), {
              year: "numeric",
              month: "short",
              day: "numeric",
            }),
        },
      ]}
      description={t("header.description")}
      eyebrow={t("header.eyebrow")}
      load={listUsers}
      queryKey="users"
      resourceLabel={t("resourceLabel")}
      title={t("header.title")}
    />
  );
}

function isUserStatus(value: unknown): value is "active" | "disabled" {
  return value === "active" || value === "disabled";
}
