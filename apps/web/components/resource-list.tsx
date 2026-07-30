"use client";

import { useQuery } from "@tanstack/react-query";
import { PageHeader } from "@/components/page-header";
import { api, type Page } from "@/lib/api";
import "@/components/page-header.css";

type Row = Record<string, unknown>;

export function ResourceList({
  endpoint,
  queryKey,
  eyebrow,
  title,
  description,
  columns,
}: {
  endpoint: string;
  queryKey: string;
  eyebrow: string;
  title: string;
  description: string;
  columns: {
    key: string;
    label: string;
    format?: (value: unknown, row: Row) => React.ReactNode;
  }[];
}) {
  const result = useQuery({
    queryKey: [queryKey],
    queryFn: () => api<Page<Row>>(endpoint),
  });

  return (
    <>
      <PageHeader description={description} eyebrow={eyebrow} title={title} />
      <section className="panel">
        {result.isPending ? (
          <div className="empty-state" aria-live="polite">
            <h2>Loading {title.toLowerCase()}…</h2>
          </div>
        ) : result.isError ? (
          <div className="empty-state" role="alert">
            <h2>{title} are unavailable</h2>
            <p>Check your permissions and API connection, then try again.</p>
          </div>
        ) : (
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  {columns.map((column) => (
                    <th key={column.key}>{column.label}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {result.data.items.map((row, index) => (
                  <tr key={String(row.id ?? index)}>
                    {columns.map((column) => (
                      <td data-label={column.label} key={column.key}>
                        {column.format
                          ? column.format(row[column.key], row)
                          : String(row[column.key] ?? "—")}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}
