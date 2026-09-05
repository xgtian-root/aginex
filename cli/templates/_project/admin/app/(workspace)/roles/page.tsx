"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Search, X } from "lucide-react";
import { useFormatter, useTranslations } from "next-intl";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageHeader } from "@/components/page-header";
import {
  type GrantScope,
  hasAllGrant,
  type PrincipalGrant,
} from "@/lib/access";
import {
  createRole,
  deleteRole,
  getCurrentUser,
  listPermissionCatalog,
  listRoles,
  type PermissionPage,
  type RolePage,
  replaceRoleGrants,
  updateRole,
} from "@/lib/api";
import { localizeApiError } from "@/lib/problem-message";
import {
  delegableScopes,
  groupPermissionsByResource,
  preferredScope,
  sameGrants,
} from "./helpers";
import "@/components/page-header.css";
import "@/components/management-console.css";

const pageSize = 20;

type RoleRecord = RolePage["items"][number];
type PermissionRecord = PermissionPage["items"][number];
type EditorState = { kind: "create" } | { kind: "edit"; role: RoleRecord };

export default function RolesPage() {
  const t = useTranslations("Roles");
  const translate = useTranslations();
  const format = useFormatter();
  const queryClient = useQueryClient();
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [editor, setEditor] = useState<EditorState | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<RoleRecord | null>(null);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setSearch(searchInput.trim());
      setPage(1);
    }, 250);
    return () => window.clearTimeout(timer);
  }, [searchInput]);

  const me = useQuery({ queryKey: ["me"], queryFn: getCurrentUser });
  const canRead = hasAllGrant(me.data, "roles:read");
  const canCreate = hasAllGrant(me.data, "roles:create");
  const canUpdate = hasAllGrant(me.data, "roles:update");
  const canReadPermissions = hasAllGrant(me.data, "permissions:read");
  const canGrant = hasAllGrant(me.data, "roles:grant") && canReadPermissions;
  const canDelete = hasAllGrant(me.data, "roles:delete");

  const roles = useQuery({
    queryKey: ["roles", { page, search }],
    queryFn: () => listRoles({ page, pageSize, ...(search ? { search } : {}) }),
    enabled: canRead,
  });
  const permissions = useQuery({
    queryKey: ["permissions", "role-editor"],
    queryFn: listPermissionCatalog,
    enabled: canGrant && editor !== null,
  });

  const editorMutation = useMutation({
    mutationFn: async (input: {
      name: string;
      description: string;
      grants: { permissionCode: string; scope: GrantScope }[];
    }) => {
      if (editor?.kind === "create") {
        await createRole(input);
        return "created" as const;
      }
      if (editor?.kind !== "edit") return "updated" as const;

      if (
        canUpdate &&
        (input.name !== editor.role.name ||
          input.description !== editor.role.description)
      ) {
        await updateRole(editor.role.id, {
          name: input.name,
          description: input.description,
        });
      }
      if (canGrant && !sameGrants(input.grants, editor.role.grants)) {
        await replaceRoleGrants(editor.role.id, { grants: input.grants });
      }
      return "updated" as const;
    },
    onSuccess: (outcome) => {
      invalidateRoleQueries(queryClient);
      setEditor(null);
      toast.success(
        outcome === "created" ? t("toasts.created") : t("toasts.updated"),
      );
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async (role: RoleRecord) => deleteRole(role.id),
    onSuccess: () => {
      invalidateRoleQueries(queryClient);
      setDeleteTarget(null);
      toast.success(t("toasts.deleted"));
    },
    onError: (error) => {
      toast.error(localizeApiError(error, translate, t("errors.delete")));
    },
  });

  if (me.isPending) {
    return <PageLoading message={t("list.loading")} />;
  }
  if (me.isError || !me.data) {
    return (
      <PageFailure
        action={
          <button
            className="button secondary"
            onClick={() => me.refetch()}
            type="button"
          >
            {t("list.retry")}
          </button>
        }
        description={t("list.unavailableDescription")}
        title={t("list.unavailableTitle")}
      />
    );
  }
  if (!canRead) {
    return (
      <PageFailure
        description={t("list.forbiddenDescription")}
        title={t("list.forbiddenTitle")}
      />
    );
  }

  const total = roles.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const editorKey =
    editor?.kind === "create"
      ? "create"
      : editor
        ? `edit-${editor.role.id}`
        : "closed";

  return (
    <div className="management-console">
      <PageHeader
        action={
          canCreate ? (
            <button
              className="button"
              onClick={() => setEditor({ kind: "create" })}
              type="button"
            >
              <Plus aria-hidden size={17} />
              {t("header.createAction")}
            </button>
          ) : undefined
        }
        description={t("header.description")}
        eyebrow={t("header.eyebrow")}
        title={t("header.title")}
      />

      {editor && (
        <RoleEditor
          actorGrants={me.data.grants}
          canGrant={canGrant}
          canUpdate={canUpdate}
          error={editorMutation.error}
          key={editorKey}
          mode={editor.kind}
          onCancel={() => setEditor(null)}
          onRetryPermissions={() => permissions.refetch()}
          onSubmit={(input) => editorMutation.mutate(input)}
          pending={editorMutation.isPending}
          permissions={permissions.data ?? []}
          permissionsError={permissions.isError}
          permissionsLoading={permissions.isPending}
          role={editor.kind === "edit" ? editor.role : undefined}
        />
      )}

      <section
        className="management-list panel"
        aria-label={t("list.ariaLabel")}
      >
        <div className="management-toolbar">
          <div className="management-filters">
            <label className="search-box">
              <Search aria-hidden size={17} />
              <span className="sr-only">{t("list.searchLabel")}</span>
              <input
                onChange={(event) => setSearchInput(event.target.value)}
                placeholder={t("list.searchPlaceholder")}
                type="search"
                value={searchInput}
              />
            </label>
          </div>
          <span className="muted">
            {t("list.recordCount", { count: total })}
          </span>
        </div>

        {roles.isPending ? (
          <PageLoading message={t("list.loading")} />
        ) : roles.isError ? (
          <PageFailure
            action={
              <button
                className="button secondary"
                onClick={() => roles.refetch()}
                type="button"
              >
                {t("list.retry")}
              </button>
            }
            description={t("list.unavailableDescription")}
            title={t("list.unavailableTitle")}
          />
        ) : roles.data.items.length === 0 ? (
          <div className="empty-state">
            <h2>{search ? t("list.noResultsTitle") : t("list.emptyTitle")}</h2>
            <p>
              {search
                ? t("list.noResultsDescription")
                : t("list.emptyDescription")}
            </p>
            {search ? (
              <button
                className="button secondary"
                onClick={() => {
                  setSearchInput("");
                  setSearch("");
                  setPage(1);
                }}
                type="button"
              >
                {t("list.clearSearch")}
              </button>
            ) : canCreate ? (
              <button
                className="button"
                onClick={() => setEditor({ kind: "create" })}
                type="button"
              >
                {t("header.createAction")}
              </button>
            ) : null}
          </div>
        ) : (
          <>
            <div className="table-scroll">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>{t("list.columns.role")}</th>
                    <th>{t("list.columns.people")}</th>
                    <th>{t("list.columns.grants")}</th>
                    <th>{t("list.columns.updated")}</th>
                    <th>
                      <span className="sr-only">
                        {t("list.columns.actions")}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {roles.data.items.map((role) => (
                    <tr key={role.id}>
                      <td data-label={t("list.columns.role")}>
                        <span className="row-title">
                          <strong>{role.name}</strong>
                          <small>{role.description}</small>
                          {role.systemManaged && (
                            <span className="record-meta">
                              {t("list.systemManaged")}
                            </span>
                          )}
                        </span>
                      </td>
                      <td data-label={t("list.columns.people")}>
                        {t("list.peopleCount", { count: role.userCount })}
                        {role.userCount > 0 && !role.systemManaged && (
                          <div className="record-meta">{t("list.inUse")}</div>
                        )}
                      </td>
                      <td data-label={t("list.columns.grants")}>
                        {t("grantCount", { count: role.grants.length })}
                      </td>
                      <td data-label={t("list.columns.updated")}>
                        {format.dateTime(new Date(role.updatedAt), {
                          year: "numeric",
                          month: "short",
                          day: "numeric",
                        })}
                      </td>
                      <td
                        className="actions-cell"
                        data-label={t("list.columns.actions")}
                      >
                        <div className="row-actions">
                          {!role.systemManaged && (canUpdate || canGrant) && (
                            <button
                              className="row-action"
                              onClick={() => setEditor({ kind: "edit", role })}
                              type="button"
                            >
                              {t("actions.edit")}
                            </button>
                          )}
                          {!role.systemManaged && canDelete && (
                            <button
                              aria-describedby={
                                role.userCount > 0
                                  ? `role-in-use-${role.id}`
                                  : undefined
                              }
                              className="row-action danger"
                              disabled={role.userCount > 0}
                              onClick={() => setDeleteTarget(role)}
                              type="button"
                            >
                              {t("actions.delete")}
                            </button>
                          )}
                          {role.userCount > 0 && !role.systemManaged && (
                            <span
                              className="sr-only"
                              id={`role-in-use-${role.id}`}
                            >
                              {t("list.deleteBlocked")}
                            </span>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="pagination">
              <span className="muted">
                {t("list.page", { page, totalPages })}
              </span>
              <div className="pagination__actions">
                <button
                  className="button secondary"
                  disabled={page <= 1}
                  onClick={() => setPage((value) => value - 1)}
                  type="button"
                >
                  {t("list.previous")}
                </button>
                <button
                  className="button secondary"
                  disabled={page >= totalPages}
                  onClick={() => setPage((value) => value + 1)}
                  type="button"
                >
                  {t("list.next")}
                </button>
              </div>
            </div>
          </>
        )}
      </section>

      {deleteTarget && (
        <ConfirmDialog
          busy={deleteMutation.isPending}
          cancelLabel={t("confirm.cancel")}
          confirmLabel={t("confirm.delete.action")}
          description={t("confirm.delete.description", {
            name: deleteTarget.name,
          })}
          destructive
          onCancel={() => setDeleteTarget(null)}
          onConfirm={() => deleteMutation.mutate(deleteTarget)}
          open
          title={t("confirm.delete.title")}
        />
      )}
    </div>
  );
}

function RoleEditor({
  actorGrants,
  mode,
  role,
  permissions,
  permissionsLoading,
  permissionsError,
  canUpdate,
  canGrant,
  pending,
  error,
  onSubmit,
  onCancel,
  onRetryPermissions,
}: {
  actorGrants: readonly PrincipalGrant[];
  mode: "create" | "edit";
  role?: RoleRecord;
  permissions: PermissionRecord[];
  permissionsLoading: boolean;
  permissionsError: boolean;
  canUpdate: boolean;
  canGrant: boolean;
  pending: boolean;
  error: unknown;
  onSubmit: (input: {
    name: string;
    description: string;
    grants: { permissionCode: string; scope: GrantScope }[];
  }) => void;
  onCancel: () => void;
  onRetryPermissions: () => void;
}) {
  const t = useTranslations("Roles");
  const translate = useTranslations();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [permissionSearch, setPermissionSearch] = useState("");
  useEffect(() => headingRef.current?.focus(), []);

  const visiblePermissions = permissions.filter((permission) => {
    const current = role?.grants.find(
      (grant) => grant.permission.code === permission.code,
    );
    if (!current && delegableScopes(permission, actorGrants).length === 0) {
      return false;
    }
    const needle = permissionSearch.trim().toLocaleLowerCase();
    return (
      !needle ||
      permission.code.toLocaleLowerCase().includes(needle) ||
      permission.description.toLocaleLowerCase().includes(needle)
    );
  });
  const groups = groupPermissionsByResource(visiblePermissions);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const grants = canGrant
      ? permissions.flatMap((permission) => {
          const current = role?.grants.find(
            (grant) => grant.permission.code === permission.code,
          );
          const scopes = delegableScopes(permission, actorGrants);
          const currentIsDelegable =
            current !== undefined &&
            scopes.includes(current.scope as GrantScope);
          if (current && !currentIsDelegable) {
            return [
              {
                permissionCode: permission.code,
                scope: current.scope as GrantScope,
              },
            ];
          }
          if (!data.has(`permission:${permission.id}`)) return [];
          const scope = String(
            data.get(`scope:${permission.id}`),
          ) as GrantScope;
          return scopes.includes(scope)
            ? [{ permissionCode: permission.code, scope }]
            : [];
        })
      : (role?.grants.map((grant) => ({
          permissionCode: grant.permission.code,
          scope: grant.scope as GrantScope,
        })) ?? []);
    onSubmit({
      name: String(data.get("name") ?? "").trim(),
      description: String(data.get("description") ?? "").trim(),
      grants,
    });
  }

  const apiError = error
    ? localizeApiError(error, translate, t("errors.save"))
    : "";

  return (
    <form className="management-editor panel" onSubmit={submit}>
      <div className="management-editor__heading">
        <div>
          <p className="eyebrow">{t(`editor.${mode}.eyebrow`)}</p>
          <h2 ref={headingRef} tabIndex={-1}>
            {t(`editor.${mode}.title`)}
          </h2>
          <p>{t(`editor.${mode}.description`)}</p>
        </div>
        <button
          aria-label={t("editor.close")}
          className="icon-button"
          onClick={onCancel}
          type="button"
        >
          <X aria-hidden size={18} />
        </button>
      </div>
      {apiError && (
        <div className="form-error" role="alert">
          {apiError}
        </div>
      )}
      <div className="management-form-grid">
        <div className="field">
          <label htmlFor={`${mode}-role-name`}>{t("editor.name")}</label>
          <input
            className="input"
            defaultValue={role?.name ?? ""}
            disabled={mode === "edit" && !canUpdate}
            id={`${mode}-role-name`}
            name="name"
            required
          />
        </div>
        <div className="field">
          <label htmlFor={`${mode}-role-description`}>
            {t("editor.description")}
          </label>
          <input
            className="input"
            defaultValue={role?.description ?? ""}
            disabled={mode === "edit" && !canUpdate}
            id={`${mode}-role-description`}
            name="description"
            required
          />
        </div>
      </div>
      {canGrant && (
        <fieldset className="management-editor__section">
          <legend>{t("editor.permissionsLegend")}</legend>
          <p className="help-text">{t("editor.permissionsDescription")}</p>
          <label className="search-box">
            <Search aria-hidden size={17} />
            <span className="sr-only">{t("editor.permissionSearchLabel")}</span>
            <input
              onChange={(event) => setPermissionSearch(event.target.value)}
              placeholder={t("editor.permissionSearchPlaceholder")}
              type="search"
              value={permissionSearch}
            />
          </label>
          {permissionsLoading ? (
            <p aria-live="polite">{t("editor.permissionsLoading")}</p>
          ) : permissionsError ? (
            <div className="form-error" role="alert">
              <span>{t("editor.permissionsError")}</span>{" "}
              <button
                className="row-action"
                onClick={onRetryPermissions}
                type="button"
              >
                {t("list.retry")}
              </button>
            </div>
          ) : visiblePermissions.length === 0 ? (
            <p className="help-text">
              {permissionSearch
                ? t("editor.noPermissionResults")
                : t("editor.noPermissions")}
            </p>
          ) : (
            <div className="permission-groups">
              {groups.map(([resource, options]) => (
                <section className="permission-group" key={resource}>
                  <h3>{resource}</h3>
                  {options.map((permission) => {
                    const current = role?.grants.find(
                      (grant) => grant.permission.code === permission.code,
                    );
                    const scopes = delegableScopes(permission, actorGrants);
                    const locked =
                      current !== undefined &&
                      !scopes.includes(current.scope as GrantScope);
                    const defaultScope =
                      current?.scope ?? preferredScope(scopes);
                    return (
                      <label className="permission-option" key={permission.id}>
                        <input
                          defaultChecked={Boolean(current)}
                          disabled={locked}
                          name={`permission:${permission.id}`}
                          type="checkbox"
                        />
                        <span className="permission-option__copy">
                          <strong>{permission.code}</strong>
                          <small>{permission.description}</small>
                        </span>
                        {locked ? (
                          <span className="record-meta">
                            {t("editor.lockedGrant", {
                              scope: t(`scopes.${defaultScope}`),
                            })}
                          </span>
                        ) : (
                          <select
                            aria-label={t("editor.scopeLabel", {
                              permission: permission.code,
                            })}
                            className="permission-option__scope"
                            defaultValue={defaultScope}
                            name={`scope:${permission.id}`}
                          >
                            {scopes.map((scope) => (
                              <option key={scope} value={scope}>
                                {t(`scopes.${scope}`)}
                              </option>
                            ))}
                          </select>
                        )}
                      </label>
                    );
                  })}
                </section>
              ))}
            </div>
          )}
        </fieldset>
      )}
      {mode === "create" && !canGrant && (
        <p className="help-text">{t("editor.grantsUnavailable")}</p>
      )}
      <div className="management-editor__actions">
        <button className="button secondary" onClick={onCancel} type="button">
          {t("editor.cancel")}
        </button>
        <button
          className="button"
          disabled={pending || (canGrant && permissionsError)}
          type="submit"
        >
          {pending ? t("editor.saving") : t(`editor.${mode}.submit`)}
        </button>
      </div>
    </form>
  );
}

function PageLoading({ message }: { message: string }) {
  return (
    <div className="empty-state" aria-live="polite">
      <p>{message}</p>
    </div>
  );
}

function PageFailure({
  title,
  description,
  action,
}: {
  title: string;
  description: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="management-error" role="alert">
      <h2>{title}</h2>
      <p>{description}</p>
      {action}
    </div>
  );
}

function invalidateRoleQueries(queryClient: ReturnType<typeof useQueryClient>) {
  queryClient.invalidateQueries({ queryKey: ["roles"] });
  queryClient.invalidateQueries({ queryKey: ["users"] });
  queryClient.invalidateQueries({ queryKey: ["me"] });
}
