"use client";

import { ResourceList } from "@/components/resource-list";
import { listRoles } from "@/lib/api";
import "../products/products.css";

export default function RolesPage() {
  return (
    <ResourceList
      columns={[
        { key: "name", label: "Role" },
        { key: "description", label: "Description" },
        {
          key: "permissions",
          label: "Permissions",
          format: (value) =>
            Array.isArray(value) ? `${value.length} grants` : "0 grants",
        },
      ]}
      description="Roles group explicit resource:action grants. Navigation and API enforcement share the same permission codes."
      eyebrow="Authorization"
      load={listRoles}
      queryKey="roles"
      title="Access"
    />
  );
}
