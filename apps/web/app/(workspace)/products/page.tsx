"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Search, Trash2, X } from "lucide-react";
import { type FormEvent, useState } from "react";
import { toast } from "sonner";
import { PageHeader } from "@/components/page-header";
import {
  ApiError,
  createProduct as createProductRequest,
  deleteProduct as deleteProductRequest,
  listProducts,
  type ProductDraft,
} from "@/lib/api";
import "@/components/page-header.css";
import "./products.css";

export default function ProductsPage() {
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
      toast.success("Product created.");
    },
    onError: (error) => {
      toast.error(
        error instanceof ApiError
          ? error.problem.detail
          : "The product could not be created.",
      );
    },
  });

  const deleteProduct = useMutation({
    mutationFn: deleteProductRequest,
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ["products"] });
      toast.success("Product deleted.");
    },
    onError: () => toast.error("The product could not be deleted."),
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
            Create product
          </button>
        }
        description="The canonical Aginex resource: searchable, permission-aware, audited, and backed by all supported databases."
        eyebrow="Catalog operations"
        title="Products"
      />

      {creating && (
        <form className="product-editor panel" onSubmit={create}>
          <div className="editor-heading">
            <div>
              <p className="eyebrow">New record</p>
              <h2>Create product</h2>
            </div>
            <button
              aria-label="Close product editor"
              className="icon-button"
              onClick={() => setCreating(false)}
              type="button"
            >
              <X aria-hidden size={18} />
            </button>
          </div>
          <div className="field">
            <label htmlFor="name">Product name</label>
            <input className="input" id="name" name="name" required />
          </div>
          <div className="field">
            <label htmlFor="sku">SKU</label>
            <input className="input" id="sku" name="sku" required />
          </div>
          <div className="field">
            <label htmlFor="price">Price</label>
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
            <label htmlFor="status">Status</label>
            <select
              className="input"
              defaultValue="draft"
              id="status"
              name="status"
            >
              <option value="draft">Draft</option>
              <option value="active">Active</option>
              <option value="archived">Archived</option>
            </select>
          </div>
          <div className="editor-actions">
            <button
              className="button secondary"
              onClick={() => setCreating(false)}
              type="button"
            >
              Keep browsing
            </button>
            <button
              className="button"
              disabled={createProduct.isPending}
              type="submit"
            >
              {createProduct.isPending ? "Creating product…" : "Create product"}
            </button>
          </div>
        </form>
      )}

      <section className="panel product-list" aria-label="Product list">
        <div className="table-tools">
          <label className="search-box">
            <Search aria-hidden size={17} />
            <span className="sr-only">Search products</span>
            <input
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search name or SKU"
              type="search"
              value={search}
            />
          </label>
          <span className="muted">{products.data?.total ?? 0} records</span>
        </div>
        {products.isError ? (
          <div className="empty-state" role="alert">
            <h2>Products are unavailable</h2>
            <p>Check the API connection and try again.</p>
          </div>
        ) : products.data?.items.length === 0 ? (
          <div className="empty-state">
            <h2>No products yet</h2>
            <p>
              Create the first product to verify the complete resource workflow.
            </p>
          </div>
        ) : (
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>SKU</th>
                  <th>Price</th>
                  <th>Status</th>
                  <th>Updated</th>
                  <th>
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {products.data?.items.map((product) => (
                  <tr key={product.id}>
                    <td data-label="Name">
                      <strong>{product.name}</strong>
                    </td>
                    <td data-label="SKU">
                      <code>{product.sku}</code>
                    </td>
                    <td data-label="Price">
                      $
                      {(product.priceCents / 100).toLocaleString(undefined, {
                        minimumFractionDigits: 2,
                      })}
                    </td>
                    <td data-label="Status">
                      <span className="status">{product.status}</span>
                    </td>
                    <td data-label="Updated">
                      {new Date(product.updatedAt).toLocaleDateString()}
                    </td>
                    <td data-label="Actions">
                      <button
                        aria-label={`Delete ${product.name}`}
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
