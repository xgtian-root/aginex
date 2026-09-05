"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Search, Trash2, X } from "lucide-react";
import { useFormatter, useTranslations } from "next-intl";
import { type FormEvent, useState } from "react";
import { toast } from "sonner";
import { PageHeader } from "@/components/page-header";
import {
  createProduct as createProductRequest,
  deleteProduct as deleteProductRequest,
  listProducts,
  type ProductDraft,
} from "@/lib/api";
import { localizeApiError } from "@/lib/problem-message";
import "@/components/page-header.css";
import "./products.css";

export default function ProductsPage() {
  const t = useTranslations("Products");
  const translate = useTranslations();
  const format = useFormatter();
  const client = useQueryClient();
  const [search, setSearch] = useState("");
  const [creating, setCreating] = useState(false);
  const products = useQuery({
    queryKey: ["products", search],
    queryFn: () => listProducts(search),
  });

  const createProduct = useMutation({
    mutationFn: (input: ProductDraft) => createProductRequest(input),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ["products"] });
      client.invalidateQueries({ queryKey: ["dashboard-summary"] });
      setCreating(false);
      toast.success(t("createdToast"));
    },
    onError: (error) => {
      toast.error(localizeApiError(error, translate, t("createError")));
    },
  });

  const deleteProduct = useMutation({
    mutationFn: deleteProductRequest,
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ["products"] });
      toast.success(t("deletedToast"));
    },
    onError: (error) =>
      toast.error(localizeApiError(error, translate, t("deleteError"))),
  });

  function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    createProduct.mutate({
      name: String(data.get("name")),
      sku: String(data.get("sku")),
      priceCents: Math.round(Number(data.get("price")) * 100),
      status: String(data.get("status")) as ProductDraft["status"],
    });
  }

  return (
    <>
      <PageHeader
        action={
          <button
            className="button"
            onClick={() => setCreating(true)}
            type="button"
          >
            <Plus aria-hidden size={17} />
            {t("header.createAction")}
          </button>
        }
        description={t("header.description")}
        eyebrow={t("header.eyebrow")}
        title={t("header.title")}
      />

      {creating && (
        <form className="product-editor panel" onSubmit={create}>
          <div className="editor-heading">
            <div>
              <p className="eyebrow">{t("editor.eyebrow")}</p>
              <h2>{t("editor.title")}</h2>
            </div>
            <button
              aria-label={t("editor.closeAriaLabel")}
              className="icon-button"
              onClick={() => setCreating(false)}
              type="button"
            >
              <X aria-hidden size={18} />
            </button>
          </div>
          <div className="field">
            <label htmlFor="name">{t("editor.nameLabel")}</label>
            <input className="input" id="name" name="name" required />
          </div>
          <div className="field">
            <label htmlFor="sku">{t("editor.skuLabel")}</label>
            <input className="input" id="sku" name="sku" required />
          </div>
          <div className="field">
            <label htmlFor="price">{t("editor.priceLabel")}</label>
            <input
              className="input"
              id="price"
              min="0"
              name="price"
              required
              step="0.01"
              type="number"
            />
          </div>
          <div className="field">
            <label htmlFor="status">{t("editor.statusLabel")}</label>
            <select
              className="input"
              defaultValue="draft"
              id="status"
              name="status"
            >
              <option value="draft">{t("statuses.draft")}</option>
              <option value="active">{t("statuses.active")}</option>
              <option value="archived">{t("statuses.archived")}</option>
            </select>
          </div>
          <div className="editor-actions">
            <button
              className="button secondary"
              onClick={() => setCreating(false)}
              type="button"
            >
              {t("editor.keepBrowsing")}
            </button>
            <button
              className="button"
              disabled={createProduct.isPending}
              type="submit"
            >
              {createProduct.isPending
                ? t("editor.pending")
                : t("editor.submit")}
            </button>
          </div>
        </form>
      )}

      <section className="panel product-list" aria-label={t("list.ariaLabel")}>
        <div className="table-tools">
          <label className="search-box">
            <Search aria-hidden size={17} />
            <span className="sr-only">{t("list.searchAriaLabel")}</span>
            <input
              onChange={(event) => setSearch(event.target.value)}
              placeholder={t("list.searchPlaceholder")}
              type="search"
              value={search}
            />
          </label>
          <span className="muted">
            {t("list.recordCount", { count: products.data?.total ?? 0 })}
          </span>
        </div>
        {products.isError ? (
          <div className="empty-state" role="alert">
            <h2>{t("list.unavailableTitle")}</h2>
            <p>{t("list.unavailableDescription")}</p>
          </div>
        ) : products.data?.items.length === 0 ? (
          <div className="empty-state">
            <h2>{t("list.emptyTitle")}</h2>
            <p>{t("list.emptyDescription")}</p>
          </div>
        ) : (
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>{t("list.columns.name")}</th>
                  <th>{t("list.columns.sku")}</th>
                  <th>{t("list.columns.price")}</th>
                  <th>{t("list.columns.status")}</th>
                  <th>{t("list.columns.updated")}</th>
                  <th>
                    <span className="sr-only">{t("list.columns.actions")}</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {products.data?.items.map((product) => (
                  <tr key={product.id}>
                    <td data-label={t("list.columns.name")}>
                      <strong>{product.name}</strong>
                    </td>
                    <td data-label={t("list.columns.sku")}>
                      <code>{product.sku}</code>
                    </td>
                    <td data-label={t("list.columns.price")}>
                      {format.number(product.priceCents / 100, {
                        style: "currency",
                        currency: "USD",
                      })}
                    </td>
                    <td data-label={t("list.columns.status")}>
                      <span className="status">
                        {t(`statuses.${product.status}`)}
                      </span>
                    </td>
                    <td data-label={t("list.columns.updated")}>
                      {format.dateTime(new Date(product.updatedAt), {
                        year: "numeric",
                        month: "short",
                        day: "numeric",
                      })}
                    </td>
                    <td data-label={t("list.columns.actions")}>
                      <button
                        aria-label={t("list.deleteAriaLabel", {
                          name: product.name,
                        })}
                        className="icon-button delete-button"
                        disabled={deleteProduct.isPending}
                        onClick={() => deleteProduct.mutate(product.id)}
                        type="button"
                      >
                        <Trash2 aria-hidden size={16} />
                      </button>
                    </td>
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
