"use client";

import { useTranslations } from "next-intl";
import { ResourceList } from "@/components/resource-list";
import { listRoles } from "@/lib/api";
import "../products/products.css";

export default function RolesPage() {
  const t = useTranslations("Roles");

  return (
    <ResourceList
      columns={[
        { key: "name", label: t("columns.role") },
        { key: "description", label: t("columns.description") },
        {
          key: "permissions",
          label: t("columns.permissions"),
          format: (value) =>
            t("grantCount", { count: Array.isArray(value) ? value.length : 0 }),
        },
      ]}
      description={t("header.description")}
      eyebrow={t("header.eyebrow")}
      load={listRoles}
      queryKey="roles"
      resourceLabel={t("resourceLabel")}
      title={t("header.title")}
    />
  );
}
