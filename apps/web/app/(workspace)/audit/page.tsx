"use client";

import { ResourceList } from "@/components/resource-list";
import { listAuditLogs } from "@/lib/api";
import "../products/products.css";

export default function AuditPage() {
  return (
    <ResourceList
      columns={[
        { key: "action", label: "Action" },
        { key: "resource", label: "Resource" },
        { key: "summary", label: "Summary" },
        { key: "ipAddress", label: "IP address" },
        {
          key: "createdAt",
          label: "Time",
          format: (value) => new Date(String(value)).toLocaleString(),
        },
      ]}
      description="A chronological record of security-sensitive and data-changing actions, tied to request IDs."
      eyebrow="Accountability"
      load={listAuditLogs}
      queryKey="audit-logs"
      title="Audit trail"
    />
  );
}
