"use client";

import { useQuery } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { PageHeader } from "@/components/page-header";
import "@/components/page-header.css";

type Row = Record<string, unknown>;

export function ResourceList({
  load,
  queryKey,
  eyebrow,
  title,
  description,
  resourceLabel,
  columns,
}: {
  load: () => Promise<{ items: object[] | null }>;
  queryKey: string;
  eyebrow: string;
  title: string;
  description: string;
  resourceLabel: string;
  columns: {
    key: string;
    label: string;
    format?: (value: unknown, row: Row) => React.ReactNode;
  }[];
}) {
  const t = useTranslations("ResourceList");
  const common = useTranslations("Common");
  const result = useQuery({
    queryKey: [queryKey],
    queryFn: load,
  });

  return (
    <>
      <PageHeader description={description} eyebrow={eyebrow} title={title} />
      <section className="panel">
        {result.isPending ? (
          <div className="empty-state" aria-live="polite">
            <h2>{t("loading", { resource: resourceLabel })}</h2>
          </div>
        ) : result.isError ? (
          <div className="empty-state" role="alert">
            <h2>{t("unavailableTitle", { resource: resourceLabel })}</h2>
            <p>{t("unavailableDescription")}</p>
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
                {(result.data.items ?? []).map((item, index) => {
                  const row = item as Row;
                  return (
                    <tr key={String(row.id ?? index)}>
                      {columns.map((column) => (
                        <td data-label={column.label} key={column.key}>
                          {column.format
                            ? column.format(row[column.key], row)
                            : String(row[column.key] ?? common("notAvailable"))}
                        </td>
                      ))}
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}
