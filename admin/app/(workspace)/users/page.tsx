"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Search, X } from "lucide-react";
import { useFormatter, useTranslations } from "next-intl";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageHeader } from "@/components/page-header";
import { hasAllGrant } from "@/lib/access";
import {
  createUser,
  deleteUser,
  disableUser,
  enableUser,
  getCurrentUser,
  grantUserAdministrator,
  listRoleCatalog,
  listUsers,
  type RolePage,
  replaceUserRoles,
  resetUserPassword,
  revokeUserAdministrator,
  type UserPage,
  updateUser,
} from "@/lib/api";
import { localizeApiError } from "@/lib/problem-message";
import { sameIDs } from "./helpers";
import "@/components/page-header.css";
import "@/components/management-console.css";

const pageSize = 20;

type UserRecord = UserPage["items"][number];
type RoleRecord = RolePage["items"][number];
type EditorState =
  | { kind: "create" }
  | { kind: "edit"; user: UserRecord }
  | { kind: "password"; user: UserRecord };
type ConfirmAction = {
  kind: "enable" | "disable" | "delete" | "grantAdmin" | "revokeAdmin";
  user: UserRecord;
};

export default function UsersPage() {
  const t = useTranslations("Users");
  const translate = useTranslations();
  const format = useFormatter();
  const queryClient = useQueryClient();
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState<"" | "active" | "disabled">("");
  const [roleId, setRoleId] = useState("");
  const [page, setPage] = useState(1);
  const [editor, setEditor] = useState<EditorState | null>(null);
  const [confirmAction, setConfirmAction] = useState<ConfirmAction | null>(
    null,
  );

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setSearch(searchInput.trim());
      setPage(1);
    }, 250);
    return () => window.clearTimeout(timer);
  }, [searchInput]);

  const me = useQuery({ queryKey: ["me"], queryFn: getCurrentUser });
  const canRead = hasAllGrant(me.data, "users:read");
  const canCreate = hasAllGrant(me.data, "users:create");
  const canUpdate = hasAllGrant(me.data, "users:update");
  const canAssignRoles = hasAllGrant(me.data, "users:assign-roles");
  const canEnable = hasAllGrant(me.data, "users:enable");
  const canDisable = hasAllGrant(me.data, "users:disable");
  const canDelete = hasAllGrant(me.data, "users:delete");
  const canResetPassword = hasAllGrant(me.data, "users:reset-password");
  const canGrantAdministrator =
    me.data?.administrator === true &&
    hasAllGrant(me.data, "users:grant-administrator");
  const canRevokeAdministrator =
    me.data?.administrator === true &&
    hasAllGrant(me.data, "users:revoke-administrator");
  const canReadRoles = hasAllGrant(me.data, "roles:read");
  const canManageRoles = canAssignRoles && canReadRoles;

  const users = useQuery({
    queryKey: ["users", { page, roleId, search, status }],
    queryFn: () =>
      listUsers({
        page,
        pageSize,
        ...(search ? { search } : {}),
        ...(status ? { status } : {}),
        ...(roleId ? { roleId } : {}),
      }),
    enabled: canRead,
  });
  const roles = useQuery({
    queryKey: ["roles", "options"],
    queryFn: listRoleCatalog,
    enabled: canReadRoles,
  });

  const editorMutation = useMutation({
    mutationFn: async (input: {
      displayName: string;
      email?: string;
      password?: string;
      roleIds: string[];
    }) => {
      if (editor?.kind === "create") {
        if (input.email === undefined || input.password === undefined) {
          throw new Error("Create-user fields are missing.");
        }
        await createUser({
          displayName: input.displayName,
          email: input.email,
          password: input.password,
          roleIds: input.roleIds,
        });
        return "created" as const;
      }
      if (editor?.kind !== "edit") return "updated" as const;

      if (canUpdate && input.displayName !== editor.user.displayName) {
        await updateUser(editor.user.id, { displayName: input.displayName });
      }
      if (
        canManageRoles &&
        !sameIDs(
          input.roleIds,
          editor.user.roles.map((role) => role.id),
        )
      ) {
        await replaceUserRoles(editor.user.id, { roleIds: input.roleIds });
      }
      return "updated" as const;
    },
    onSuccess: (outcome) => {
      invalidateAccessQueries(queryClient);
      setEditor(null);
      toast.success(
        outcome === "created" ? t("toasts.created") : t("toasts.updated"),
      );
    },
  });

  const passwordMutation = useMutation({
    mutationFn: async (input: { password: string }) => {
      if (editor?.kind !== "password") return;
      await resetUserPassword(editor.user.id, input);
    },
    onSuccess: () => {
      invalidateAccessQueries(queryClient);
      setEditor(null);
      toast.success(t("toasts.passwordReset"));
    },
  });

  const actionMutation = useMutation({
    mutationFn: async (action: ConfirmAction) => {
      switch (action.kind) {
        case "enable":
          await enableUser(action.user.id);
          return;
        case "disable":
          await disableUser(action.user.id);
          return;
        case "delete":
          await deleteUser(action.user.id);
          return;
        case "grantAdmin":
          await grantUserAdministrator(action.user.id);
          return;
        case "revokeAdmin":
          await revokeUserAdministrator(action.user.id);
      }
    },
    onSuccess: (_result, action) => {
      invalidateAccessQueries(queryClient);
      setConfirmAction(null);
      toast.success(t(`toasts.${action.kind}`));
    },
    onError: (error) => {
      toast.error(localizeApiError(error, translate, t("errors.action")));
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

  const total = users.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const hasFilters = Boolean(search || status || roleId);
  const editorKey =
    editor?.kind === "create"
      ? "create"
      : editor
        ? `${editor.kind}-${editor.user.id}`
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

      {editor?.kind === "password" ? (
        <PasswordEditor
          error={passwordMutation.error}
          key={editorKey}
          onCancel={() => setEditor(null)}
          onSubmit={(password) => passwordMutation.mutate({ password })}
          pending={passwordMutation.isPending}
          user={editor.user}
        />
      ) : editor ? (
        <UserEditor
          canAssignRoles={
            canManageRoles &&
            (editor.kind === "create" ||
              (!editor.user.administrator && editor.user.id !== me.data.id))
          }
          canUpdate={canUpdate}
          error={editorMutation.error}
          key={editorKey}
          mode={editor.kind}
          onCancel={() => setEditor(null)}
          onRetryRoles={() => roles.refetch()}
          onSubmit={(input) => editorMutation.mutate(input)}
          pending={editorMutation.isPending}
          roles={(roles.data ?? []).filter((role) => !role.systemManaged)}
          rolesError={roles.isError}
          rolesLoading={roles.isPending}
          user={editor.kind === "edit" ? editor.user : undefined}
        />
      ) : null}

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
            <label>
              <span className="sr-only">{t("list.statusFilterLabel")}</span>
              <select
                className="compact-select"
                onChange={(event) => {
                  setStatus(event.target.value as typeof status);
                  setPage(1);
                }}
                value={status}
              >
                <option value="">{t("list.allStatuses")}</option>
                <option value="active">{t("statuses.active")}</option>
                <option value="disabled">{t("statuses.disabled")}</option>
              </select>
            </label>
            {canReadRoles && (
              <label>
                <span className="sr-only">{t("list.roleFilterLabel")}</span>
                <select
                  className="compact-select"
                  onChange={(event) => {
                    setRoleId(event.target.value);
                    setPage(1);
                  }}
                  value={roleId}
                >
                  <option value="">{t("list.allRoles")}</option>
                  {(roles.data ?? []).map((role) => (
                    <option key={role.id} value={role.id}>
                      {role.name}
                    </option>
                  ))}
                </select>
              </label>
            )}
          </div>
          <span className="muted">
            {t("list.recordCount", { count: total })}
          </span>
        </div>

        {users.isPending ? (
          <PageLoading message={t("list.loading")} />
        ) : users.isError ? (
          <PageFailure
            action={
              <button
                className="button secondary"
                onClick={() => users.refetch()}
                type="button"
              >
                {t("list.retry")}
              </button>
            }
            description={t("list.unavailableDescription")}
            title={t("list.unavailableTitle")}
          />
        ) : users.data.items.length === 0 ? (
          <div className="empty-state">
            <h2>
              {hasFilters ? t("list.noResultsTitle") : t("list.emptyTitle")}
            </h2>
            <p>
              {hasFilters
                ? t("list.noResultsDescription")
                : t("list.emptyDescription")}
            </p>
            {hasFilters ? (
              <button
                className="button secondary"
                onClick={() => {
                  setSearchInput("");
                  setSearch("");
                  setStatus("");
                  setRoleId("");
                  setPage(1);
                }}
                type="button"
              >
                {t("list.clearFilters")}
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
                    <th>{t("list.columns.person")}</th>
                    <th>{t("list.columns.status")}</th>
                    <th>{t("list.columns.roles")}</th>
                    <th>{t("list.columns.updated")}</th>
                    <th>
                      <span className="sr-only">
                        {t("list.columns.actions")}
                      </span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {users.data.items.map((user) => {
                    const isCurrent = user.id === me.data.id;
                    return (
                      <tr key={user.id}>
                        <td data-label={t("list.columns.person")}>
                          <span className="row-title">
                            <strong>{user.displayName}</strong>
                            <small>{user.email}</small>
                          </span>
                        </td>
                        <td data-label={t("list.columns.status")}>
                          <span className={`status ${user.status}`}>
                            {t(`statuses.${user.status}`)}
                          </span>
                          {isCurrent && (
                            <div className="record-meta">
                              {t("list.currentAccount")}
                            </div>
                          )}
                        </td>
                        <td data-label={t("list.columns.roles")}>
                          <div className="role-chips">
                            {user.roles.length === 0 ? (
                              <span className="muted">{t("list.noRoles")}</span>
                            ) : (
                              user.roles.map((role) => (
                                <span
                                  className={
                                    role.systemManaged
                                      ? "role-chip system"
                                      : "role-chip"
                                  }
                                  key={role.id}
                                >
                                  {role.name}
                                </span>
                              ))
                            )}
                          </div>
                        </td>
                        <td data-label={t("list.columns.updated")}>
                          {format.dateTime(new Date(user.updatedAt), {
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
                            {(canUpdate ||
                              (canManageRoles &&
                                !isCurrent &&
                                !user.administrator)) && (
                              <button
                                className="row-action"
                                onClick={() =>
                                  setEditor({ kind: "edit", user })
                                }
                                type="button"
                              >
                                {t("actions.edit")}
                              </button>
                            )}
                            {canResetPassword &&
                              !isCurrent &&
                              !user.administrator && (
                                <button
                                  className="row-action"
                                  onClick={() =>
                                    setEditor({ kind: "password", user })
                                  }
                                  type="button"
                                >
                                  {t("actions.resetPassword")}
                                </button>
                              )}
                            {!isCurrent &&
                              !user.administrator &&
                              ((user.status === "active" && canDisable) ||
                                (user.status === "disabled" && canEnable)) && (
                                <button
                                  className="row-action"
                                  onClick={() =>
                                    setConfirmAction({
                                      kind:
                                        user.status === "active"
                                          ? "disable"
                                          : "enable",
                                      user,
                                    })
                                  }
                                  type="button"
                                >
                                  {user.status === "active"
                                    ? t("actions.disable")
                                    : t("actions.enable")}
                                </button>
                              )}
                            {!user.administrator &&
                              user.status === "active" &&
                              canGrantAdministrator && (
                                <button
                                  className="row-action"
                                  onClick={() =>
                                    setConfirmAction({
                                      kind: "grantAdmin",
                                      user,
                                    })
                                  }
                                  type="button"
                                >
                                  {t("actions.grantAdmin")}
                                </button>
                              )}
                            {user.administrator &&
                              canRevokeAdministrator &&
                              !isCurrent && (
                                <button
                                  className="row-action danger"
                                  onClick={() =>
                                    setConfirmAction({
                                      kind: "revokeAdmin",
                                      user,
                                    })
                                  }
                                  type="button"
                                >
                                  {t("actions.revokeAdmin")}
                                </button>
                              )}
                            {canDelete && !isCurrent && !user.administrator && (
                              <button
                                className="row-action danger"
                                onClick={() =>
                                  setConfirmAction({ kind: "delete", user })
                                }
                                type="button"
                              >
                                {t("actions.delete")}
                              </button>
                            )}
                          </div>
                        </td>
                      </tr>
                    );
                  })}
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

      {confirmAction && (
        <ConfirmDialog
          busy={actionMutation.isPending}
          cancelLabel={t("confirm.cancel")}
          confirmLabel={t(`confirm.${confirmAction.kind}.action`)}
          description={t(`confirm.${confirmAction.kind}.description`, {
            email: confirmAction.user.email,
            name: confirmAction.user.displayName,
          })}
          destructive={["disable", "delete", "revokeAdmin"].includes(
            confirmAction.kind,
          )}
          onCancel={() => setConfirmAction(null)}
          onConfirm={() => actionMutation.mutate(confirmAction)}
          open
          title={t(`confirm.${confirmAction.kind}.title`)}
        />
      )}
    </div>
  );
}

function UserEditor({
  mode,
  user,
  roles,
  rolesLoading,
  rolesError,
  canUpdate,
  canAssignRoles,
  pending,
  error,
  onSubmit,
  onCancel,
  onRetryRoles,
}: {
  mode: "create" | "edit";
  user?: UserRecord;
  roles: RoleRecord[];
  rolesLoading: boolean;
  rolesError: boolean;
  canUpdate: boolean;
  canAssignRoles: boolean;
  pending: boolean;
  error: unknown;
  onSubmit: (input: {
    displayName: string;
    email?: string;
    password?: string;
    roleIds: string[];
  }) => void;
  onCancel: () => void;
  onRetryRoles: () => void;
}) {
  const t = useTranslations("Users");
  const translate = useTranslations();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [validationError, setValidationError] = useState("");

  useEffect(() => headingRef.current?.focus(), []);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const password = String(data.get("password") ?? "");
    const confirmation = String(data.get("passwordConfirmation") ?? "");
    if (mode === "create" && password !== confirmation) {
      setValidationError(t("editor.passwordMismatch"));
      return;
    }
    setValidationError("");
    onSubmit({
      displayName: String(data.get("displayName") ?? "").trim(),
      ...(mode === "create"
        ? { email: String(data.get("email") ?? "").trim(), password }
        : {}),
      roleIds: canAssignRoles
        ? data.getAll("roleIds").map((value) => String(value))
        : (user?.roles.map((role) => role.id) ?? []),
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
      {(validationError || apiError) && (
        <div className="form-error" role="alert">
          {validationError || apiError}
        </div>
      )}
      <div className="management-form-grid">
        <div className="field">
          <label htmlFor={`${mode}-display-name`}>
            {t("editor.displayName")}
          </label>
          <input
            className="input"
            defaultValue={user?.displayName ?? ""}
            disabled={mode === "edit" && !canUpdate}
            id={`${mode}-display-name`}
            name="displayName"
            required
          />
        </div>
        <div className="field">
          <label htmlFor={`${mode}-email`}>{t("editor.email")}</label>
          <input
            autoComplete="off"
            className="input"
            defaultValue={user?.email ?? ""}
            disabled={mode === "edit"}
            id={`${mode}-email`}
            name="email"
            required
            type="email"
          />
          {mode === "edit" && (
            <span className="help-text">{t("editor.emailReadOnly")}</span>
          )}
        </div>
        {mode === "create" && (
          <>
            <div className="field">
              <label htmlFor="create-password">{t("editor.password")}</label>
              <input
                autoComplete="new-password"
                className="input"
                id="create-password"
                name="password"
                required
                type="password"
              />
            </div>
            <div className="field">
              <label htmlFor="create-password-confirmation">
                {t("editor.passwordConfirmation")}
              </label>
              <input
                autoComplete="new-password"
                className="input"
                id="create-password-confirmation"
                name="passwordConfirmation"
                required
                type="password"
              />
            </div>
          </>
        )}
      </div>
      {canAssignRoles && (
        <fieldset className="management-editor__section">
          <legend>{t("editor.rolesLegend")}</legend>
          <p className="help-text">{t("editor.rolesDescription")}</p>
          {rolesLoading ? (
            <p aria-live="polite">{t("editor.rolesLoading")}</p>
          ) : rolesError ? (
            <div className="form-error" role="alert">
              <span>{t("editor.rolesError")}</span>{" "}
              <button
                className="row-action"
                onClick={onRetryRoles}
                type="button"
              >
                {t("list.retry")}
              </button>
            </div>
          ) : roles.length === 0 ? (
            <p className="help-text">{t("editor.noRoles")}</p>
          ) : (
            <div className="choice-grid">
              {roles.map((role) => (
                <label className="choice-card" key={role.id}>
                  <input
                    defaultChecked={user?.roles.some(
                      (assigned) => assigned.id === role.id,
                    )}
                    name="roleIds"
                    type="checkbox"
                    value={role.id}
                  />
                  <span>
                    <strong>{role.name}</strong>
                    <small>{role.description}</small>
                  </span>
                </label>
              ))}
            </div>
          )}
        </fieldset>
      )}
      {mode === "create" && !canAssignRoles && (
        <p className="help-text">{t("editor.rolesUnavailable")}</p>
      )}
      <div className="management-editor__actions">
        <button className="button secondary" onClick={onCancel} type="button">
          {t("editor.cancel")}
        </button>
        <button
          className="button"
          disabled={pending || (canAssignRoles && rolesError)}
          type="submit"
        >
          {pending ? t("editor.saving") : t(`editor.${mode}.submit`)}
        </button>
      </div>
    </form>
  );
}

function PasswordEditor({
  user,
  pending,
  error,
  onSubmit,
  onCancel,
}: {
  user: UserRecord;
  pending: boolean;
  error: unknown;
  onSubmit: (password: string) => void;
  onCancel: () => void;
}) {
  const t = useTranslations("Users");
  const translate = useTranslations();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [validationError, setValidationError] = useState("");
  useEffect(() => headingRef.current?.focus(), []);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const password = String(data.get("password") ?? "");
    if (password !== String(data.get("passwordConfirmation") ?? "")) {
      setValidationError(t("editor.passwordMismatch"));
      return;
    }
    setValidationError("");
    onSubmit(password);
  }

  return (
    <form className="management-editor panel" onSubmit={submit}>
      <div className="management-editor__heading">
        <div>
          <p className="eyebrow">{t("passwordEditor.eyebrow")}</p>
          <h2 ref={headingRef} tabIndex={-1}>
            {t("passwordEditor.title", { name: user.displayName })}
          </h2>
          <p>{t("passwordEditor.description", { email: user.email })}</p>
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
      {(validationError || Boolean(error)) && (
        <div className="form-error" role="alert">
          {validationError ||
            localizeApiError(error, translate, t("errors.password"))}
        </div>
      )}
      <div className="management-form-grid">
        <div className="field">
          <label htmlFor="reset-password">{t("editor.password")}</label>
          <input
            autoComplete="new-password"
            className="input"
            id="reset-password"
            name="password"
            required
            type="password"
          />
        </div>
        <div className="field">
          <label htmlFor="reset-password-confirmation">
            {t("editor.passwordConfirmation")}
          </label>
          <input
            autoComplete="new-password"
            className="input"
            id="reset-password-confirmation"
            name="passwordConfirmation"
            required
            type="password"
          />
        </div>
      </div>
      <div className="management-editor__actions">
        <button className="button secondary" onClick={onCancel} type="button">
          {t("editor.cancel")}
        </button>
        <button className="button" disabled={pending} type="submit">
          {pending ? t("editor.saving") : t("passwordEditor.submit")}
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

function invalidateAccessQueries(
  queryClient: ReturnType<typeof useQueryClient>,
) {
  queryClient.invalidateQueries({ queryKey: ["users"] });
  queryClient.invalidateQueries({ queryKey: ["roles"] });
  queryClient.invalidateQueries({ queryKey: ["me"] });
}
