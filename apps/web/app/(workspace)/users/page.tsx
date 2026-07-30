"use client";

import { ResourceList } from "@/components/resource-list";
import "../products/products.css";

export default function UsersPage() {
  return (
    <ResourceList
      columns={[
        { key: "displayName", label: "Name" },
        { key: "email", label: "Email" },
        {
          key: "status",
          label: "Status",
          format: (value) => <span className="status">{String(value)}</span>,
        },
        {
          key: "createdAt",
          label: "Created",
          format: (value) => new Date(String(value)).toLocaleDateString(),
        },
      ]}
      description="Operators who can enter this instance. Public registration stays disabled by design."
      endpoint="/users"
      eyebrow="Identity"
      queryKey="users"
      title="People"
    />
  );
}
