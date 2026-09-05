"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Archive,
  CheckCircle2,
  Cloud,
  Pencil,
  Plus,
  RotateCcw,
  Server,
  Star,
  Trash2,
  UploadCloud,
  X,
} from "lucide-react";
import { useTranslations } from "next-intl";
import { type FormEvent, useState } from "react";
import { toast } from "sonner";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageHeader } from "@/components/page-header";
import { hasAllGrant } from "@/lib/access";
import {
  activateStorageProfile,
  archiveStorageProfile,
  createStorageProfile,
  deleteStorageProfile,
  getCurrentUser,
  getStorageSettings,
  listStorageProfiles,
  restoreStorageProfile,
  type StorageProfile,
  type StorageProfileDraft,
  type StorageSettings,
  testStorageProfile,
  updateFileUploadPolicy,
  updateStorageProfile,
} from "@/lib/api";
import { localizeApiError } from "@/lib/problem-message";
import "@/components/page-header.css";
import "@/components/management-console.css";
import "./settings.css";

type CloudProvider = "aliyun-oss" | "aws-s3" | "minio" | "cloudflare-r2";
type Editor = { profile?: StorageProfile } | null;
type ConfirmAction = {
  kind: "archive" | "delete";
  profile: StorageProfile;
} | null;

export default function SettingsPage() {
  const t = useTranslations("StorageSettings");
  const translate = useTranslations();
  const queryClient = useQueryClient();
  const [editor, setEditor] = useState<Editor>(null);
  const [confirm, setConfirm] = useState<ConfirmAction>(null);

  const me = useQuery({ queryKey: ["me"], queryFn: getCurrentUser });
  const canRead = hasAllGrant(me.data, "storage-profiles:read");
  const settings = useQuery({
    queryKey: ["storage-settings"],
    queryFn: getStorageSettings,
    enabled: canRead,
  });
  const profiles = useQuery({
    queryKey: ["storage-profiles"],
    queryFn: () => listStorageProfiles(),
    enabled: canRead,
  });
  const managed = settings.data?.value.environmentManaged ?? false;

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ["storage-settings"] });
    queryClient.invalidateQueries({ queryKey: ["storage-profiles"] });
  };

  const action = useMutation({
    mutationFn: async (input: {
      kind: "activate" | "archive" | "restore" | "delete";
      profile: StorageProfile;
    }) => {
      const etag = profiles.data?.etag;
      if (!etag) throw new Error(t("errors.revision"));
      if (input.kind === "activate")
        return activateStorageProfile(input.profile.id, etag);
      if (input.kind === "archive")
        return archiveStorageProfile(input.profile.id, etag);
      if (input.kind === "restore")
        return restoreStorageProfile(input.profile.id, etag);
      await deleteStorageProfile(input.profile.id, etag);
    },
    onSuccess: (_, input) => {
      refresh();
      setConfirm(null);
      toast.success(t(`toasts.${input.kind}`));
    },
    onError: (error) => {
      toast.error(localizeApiError(error, translate, t("errors.action")));
      refresh();
    },
  });

  if (me.isPending) return <PageState text={t("states.loading")} />;
  if (me.isError || !me.data)
    return <PageState error text={t("states.unavailable")} />;
  if (!canRead) return <PageState error text={t("states.forbidden")} />;

  const records = profiles.data?.value.items ?? [];
  const restartRequired = settings.data?.value.restartRequired;

  return (
    <div className="management-console storage-console">
      <PageHeader
        action={
          hasAllGrant(me.data, "storage-profiles:create") && !managed ? (
            <button
              className="button"
              onClick={() => setEditor({})}
              type="button"
            >
              <Plus size={17} />
              {t("header.create")}
            </button>
          ) : undefined
        }
        description={t("header.description")}
        eyebrow={t("header.eyebrow")}
        title={t("header.title")}
      />

      {settings.isError || profiles.isError ? (
        <PageState
          error
          text={t("states.unavailable")}
          action={
            <button
              className="button secondary"
              onClick={refresh}
              type="button"
            >
              {t("actions.retry")}
            </button>
          }
        />
      ) : settings.isPending || profiles.isPending ? (
        <PageState text={t("states.loading")} />
      ) : (
        <>
          <section
            className="storage-summary"
            aria-label={t("summary.ariaLabel")}
          >
            <div className="storage-summary__card">
              <span>{t("summary.default")}</span>
              <strong>
                {records.find((item) => item.pendingActive)?.name ?? "—"}
              </strong>
            </div>
            <div className="storage-summary__card">
              <span>{t("summary.profiles")}</span>
              <strong>{profiles.data.value.total}</strong>
            </div>
            <div className="storage-summary__card">
              <span>{t("summary.unbound")}</span>
              <strong>{settings.data.value.unboundFileCount}</strong>
            </div>
          </section>

          {managed && (
            <div className="storage-notice warning" role="status">
              <Server size={19} />
              <div>
                <strong>{t("notices.managedTitle")}</strong>
                <p>{t("notices.managedDescription")}</p>
              </div>
            </div>
          )}
          {restartRequired && (
            <div className="storage-notice" role="status">
              <RotateCcw size={19} />
              <div>
                <strong>{t("notices.restartTitle")}</strong>
                <p>{t("notices.restartDescription")}</p>
              </div>
            </div>
          )}
          {settings.data.value.unboundFileCount > 0 && (
            <div className="storage-notice danger" role="alert">
              <Archive size={19} />
              <div>
                <strong>{t("notices.unboundTitle")}</strong>
                <p>
                  {t("notices.unboundDescription", {
                    count: settings.data.value.unboundFileCount,
                  })}
                </p>
              </div>
            </div>
          )}

          <UploadPolicyEditor
            canUpdate={hasAllGrant(me.data, "storage-profiles:update")}
            etag={settings.data.etag}
            onSaved={refresh}
            settings={settings.data.value}
          />

          {editor && (
            <ProfileEditor
              canTest={hasAllGrant(me.data, "storage-profiles:test")}
              etag={profiles.data.etag}
              onClose={() => setEditor(null)}
              onSaved={() => {
                setEditor(null);
                refresh();
              }}
              profile={editor.profile}
            />
          )}

          <section className="storage-grid" aria-label={t("list.ariaLabel")}>
            {records.length === 0 ? (
              <PageState text={t("states.empty")} />
            ) : (
              records.map((profile) => (
                <article
                  className={`storage-card ${profile.pendingActive ? "selected" : ""}`}
                  key={profile.id}
                >
                  <div className="storage-card__header">
                    <span className="storage-provider-icon">
                      {profile.driver === "local" ? (
                        <Server size={20} />
                      ) : (
                        <Cloud size={20} />
                      )}
                    </span>
                    <div>
                      <h2>{profile.name}</h2>
                      <p>{t(`providers.${profile.provider}`)}</p>
                    </div>
                    <span
                      className={`status ${profile.status === "archived" ? "disabled" : "active"}`}
                    >
                      {t(`status.${profile.status}`)}
                    </span>
                  </div>
                  <dl>
                    <div>
                      <dt>{t("fields.target")}</dt>
                      <dd>{profile.bucket || profile.localRoot || "—"}</dd>
                    </div>
                    <div>
                      <dt>{t("fields.region")}</dt>
                      <dd>{profile.region || "—"}</dd>
                    </div>
                    <div>
                      <dt>{t("fields.credentials")}</dt>
                      <dd>
                        {profile.authConfigured
                          ? t("values.configured")
                          : t("values.missing")}
                      </dd>
                    </div>
                  </dl>
                  <div className="storage-card__badges">
                    {profile.pendingActive && (
                      <span>
                        <Star size={13} />
                        {t("values.pendingDefault")}
                      </span>
                    )}
                    {profile.active && (
                      <span>
                        <CheckCircle2 size={13} />
                        {t("values.runtimeDefault")}
                      </span>
                    )}
                    {profile.used && <span>{t("values.inUse")}</span>}
                  </div>
                  {!managed && (
                    <div className="row-actions">
                      {profile.provider !== "local" &&
                        hasAllGrant(me.data, "storage-profiles:update") && (
                          <button
                            className="row-action"
                            onClick={() => setEditor({ profile })}
                            type="button"
                          >
                            <Pencil size={14} />
                            {t("actions.edit")}
                          </button>
                        )}
                      {!profile.pendingActive &&
                        profile.status === "available" &&
                        hasAllGrant(me.data, "storage-profiles:activate") && (
                          <button
                            className="row-action"
                            disabled={action.isPending}
                            onClick={() =>
                              action.mutate({ kind: "activate", profile })
                            }
                            type="button"
                          >
                            <Star size={14} />
                            {t("actions.activate")}
                          </button>
                        )}
                      {profile.status === "available" &&
                        !profile.pendingActive &&
                        profile.provider !== "local" &&
                        hasAllGrant(me.data, "storage-profiles:archive") && (
                          <button
                            className="row-action"
                            onClick={() =>
                              setConfirm({ kind: "archive", profile })
                            }
                            type="button"
                          >
                            <Archive size={14} />
                            {t("actions.archive")}
                          </button>
                        )}
                      {profile.status === "archived" &&
                        profile.provider !== "local" &&
                        hasAllGrant(me.data, "storage-profiles:archive") && (
                          <button
                            className="row-action"
                            disabled={action.isPending}
                            onClick={() =>
                              action.mutate({ kind: "restore", profile })
                            }
                            type="button"
                          >
                            <RotateCcw size={14} />
                            {t("actions.restore")}
                          </button>
                        )}
                      {!profile.used &&
                        !profile.pendingActive &&
                        profile.provider !== "local" &&
                        hasAllGrant(me.data, "storage-profiles:delete") && (
                          <button
                            className="row-action danger"
                            onClick={() =>
                              setConfirm({ kind: "delete", profile })
                            }
                            type="button"
                          >
                            <Trash2 size={14} />
                            {t("actions.delete")}
                          </button>
                        )}
                    </div>
                  )}
                </article>
              ))
            )}
          </section>
        </>
      )}

      <ConfirmDialog
        busy={action.isPending}
        cancelLabel={t("actions.cancel")}
        confirmLabel={confirm ? t(`confirm.${confirm.kind}.action`) : ""}
        description={
          confirm
            ? t(`confirm.${confirm.kind}.description`, {
                name: confirm.profile.name,
              })
            : ""
        }
        destructive
        onCancel={() => setConfirm(null)}
        onConfirm={() => confirm && action.mutate(confirm)}
        open={confirm !== null}
        title={confirm ? t(`confirm.${confirm.kind}.title`) : ""}
      />
    </div>
  );
}

function UploadPolicyEditor({
  settings,
  etag,
  canUpdate,
  onSaved,
}: {
  settings: StorageSettings;
  etag: string;
  canUpdate: boolean;
  onSaved: () => void;
}) {
  const t = useTranslations("StorageSettings");
  const translate = useTranslations();
  const [maxUploadMiB, setMaxUploadMiB] = useState<number | "">(
    settings.pendingFileUploadPolicy.maxUploadBytes / (1024 * 1024),
  );
  const [resumable, setResumable] = useState(
    settings.pendingFileUploadPolicy.resumableUploadsEnabled,
  );
  const [saving, setSaving] = useState(false);
  const runtime = settings.runtimeFileUploadPolicy;
  const pending = settings.pendingFileUploadPolicy;
  const pendingDiffers =
    runtime.maxUploadBytes !== pending.maxUploadBytes ||
    runtime.resumableUploadsEnabled !== pending.resumableUploadsEnabled;
  const dirty =
    (typeof maxUploadMiB === "number" &&
      maxUploadMiB * 1024 * 1024 !== pending.maxUploadBytes) ||
    resumable !== pending.resumableUploadsEnabled;

  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!event.currentTarget.reportValidity()) return;
    if (typeof maxUploadMiB !== "number") return;
    setSaving(true);
    try {
      await updateFileUploadPolicy(
        {
          maxUploadBytes: maxUploadMiB * 1024 * 1024,
          resumableUploadsEnabled: resumable,
        },
        etag,
      );
      toast.success(t("uploadPolicy.savedToast"));
      onSaved();
    } catch (error) {
      toast.error(
        localizeApiError(error, translate, t("uploadPolicy.saveError")),
      );
      onSaved();
    } finally {
      setSaving(false);
    }
  };

  return (
    <section
      className="upload-policy panel"
      aria-labelledby="upload-policy-title"
    >
      <div className="upload-policy__heading">
        <span className="storage-provider-icon">
          <UploadCloud aria-hidden size={20} />
        </span>
        <div>
          <h2 id="upload-policy-title">{t("uploadPolicy.title")}</h2>
          <p>{t("uploadPolicy.description")}</p>
        </div>
        {!canUpdate && (
          <span className="status disabled">{t("uploadPolicy.readOnly")}</span>
        )}
      </div>

      <fieldset className="upload-policy__runtime">
        <legend className="sr-only">
          {t("uploadPolicy.comparisonAriaLabel")}
        </legend>
        <div className="upload-policy__state">
          <span>{t("uploadPolicy.runtimeLabel")}</span>
          <strong>
            {t("uploadPolicy.sizeValue", {
              size: runtime.maxUploadBytes / (1024 * 1024),
            })}
          </strong>
          <small>
            {t(
              runtime.resumableUploadsEnabled
                ? "uploadPolicy.resumableOn"
                : "uploadPolicy.resumableOff",
            )}
          </small>
        </div>
        <div
          className={`upload-policy__state ${pendingDiffers ? "pending" : ""}`}
        >
          <span>{t("uploadPolicy.pendingLabel")}</span>
          <strong>
            {t("uploadPolicy.sizeValue", {
              size: pending.maxUploadBytes / (1024 * 1024),
            })}
          </strong>
          <small>
            {t(
              pending.resumableUploadsEnabled
                ? "uploadPolicy.resumableOn"
                : "uploadPolicy.resumableOff",
            )}
          </small>
        </div>
      </fieldset>

      <form onSubmit={save}>
        <fieldset disabled={!canUpdate || saving}>
          <label className="field">
            <span>{t("uploadPolicy.maxSizeLabel")}</span>
            <span className="upload-policy__size-input">
              <input
                className="input"
                inputMode="numeric"
                max={1024}
                min={1}
                name="maxUploadMiB"
                onChange={(event) =>
                  setMaxUploadMiB(
                    event.target.value === "" ? "" : event.target.valueAsNumber,
                  )
                }
                required
                step={1}
                type="number"
                value={maxUploadMiB}
              />
              <span>{t("uploadPolicy.mibUnit")}</span>
            </span>
            <small>{t("uploadPolicy.maxSizeHint")}</small>
          </label>
          <label className="choice-card upload-policy__choice">
            <input
              checked={resumable}
              name="resumableUploadsEnabled"
              onChange={(event) => setResumable(event.target.checked)}
              type="checkbox"
            />
            <span>
              <strong>{t("uploadPolicy.resumableLabel")}</strong>
              <small>{t("uploadPolicy.resumableHint")}</small>
            </span>
          </label>
        </fieldset>
        <div className="upload-policy__footer">
          <p className={pendingDiffers || dirty ? "restart" : ""} role="status">
            <RotateCcw aria-hidden size={16} />
            {t(
              pendingDiffers || dirty
                ? "uploadPolicy.restartHint"
                : "uploadPolicy.inSyncHint",
            )}
          </p>
          {canUpdate && (
            <button
              className="button"
              disabled={!dirty || saving}
              type="submit"
            >
              {saving ? t("actions.saving") : t("uploadPolicy.saveAction")}
            </button>
          )}
        </div>
      </form>
    </section>
  );
}

function ProfileEditor({
  profile,
  etag,
  canTest,
  onClose,
  onSaved,
}: {
  profile?: StorageProfile;
  etag: string;
  canTest: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const t = useTranslations("StorageSettings");
  const translate = useTranslations();
  const [provider, setProvider] = useState<CloudProvider>(
    (profile?.provider as CloudProvider) ?? "aliyun-oss",
  );
  const [testPassed, setTestPassed] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);

  const submit = async (element: HTMLFormElement, mode: "save" | "test") => {
    if (!element.reportValidity()) return;
    const form = new FormData(element);
    const text = (name: string) => String(form.get(name) ?? "").trim();
    const draft: StorageProfileDraft = {
      ...(profile ? { id: profile.id } : {}),
      name: text("name"),
      provider,
      bucket: text("bucket"),
      region: text("region"),
      endpoint: text("endpoint"),
      accountId: text("accountId"),
      ...(text("accessKeyId") ? { accessKeyId: text("accessKeyId") } : {}),
      ...(text("accessKeySecret")
        ? { accessKeySecret: text("accessKeySecret") }
        : {}),
    };
    if (mode === "test") {
      setTesting(true);
      try {
        await testStorageProfile(draft);
        setTestPassed(true);
        toast.success(t("toasts.tested"));
      } catch (error) {
        setTestPassed(false);
        toast.error(localizeApiError(error, translate, t("errors.test")));
      } finally {
        setTesting(false);
      }
      return;
    }
    setSaving(true);
    try {
      if (profile) await updateStorageProfile(profile.id, draft, etag);
      else await createStorageProfile(draft, etag);
      const accessKeyID = element.elements.namedItem("accessKeyId");
      const accessKeySecret = element.elements.namedItem("accessKeySecret");
      if (accessKeyID instanceof HTMLInputElement) accessKeyID.value = "";
      if (accessKeySecret instanceof HTMLInputElement)
        accessKeySecret.value = "";
      toast.success(t(profile ? "toasts.updated" : "toasts.created"));
      onSaved();
    } catch (error) {
      toast.error(localizeApiError(error, translate, t("errors.save")));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form
      className="management-editor panel"
      onSubmit={(event: FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        submit(event.currentTarget, "save");
      }}
    >
      <div className="management-editor__heading">
        <div>
          <h2>{t(profile ? "editor.editTitle" : "editor.createTitle")}</h2>
          <p>{t("editor.description")}</p>
        </div>
        <button
          aria-label={t("actions.close")}
          className="icon-button"
          onClick={onClose}
          type="button"
        >
          <X size={18} />
        </button>
      </div>
      <div className="management-form-grid">
        <label className="field">
          <span>{t("fields.name")}</span>
          <input
            defaultValue={profile?.name}
            maxLength={120}
            name="name"
            required
          />
        </label>
        <label className="field">
          <span>{t("fields.provider")}</span>
          <select
            disabled={Boolean(profile)}
            name="provider"
            onChange={(event) => {
              setProvider(event.target.value as CloudProvider);
              setTestPassed(false);
              const form = event.currentTarget.form;
              for (const name of [
                "bucket",
                "region",
                "endpoint",
                "accountId",
                "accessKeyId",
                "accessKeySecret",
              ]) {
                const field = form?.elements.namedItem(name);
                if (field instanceof HTMLInputElement) field.value = "";
              }
            }}
            value={provider}
          >
            {(["aliyun-oss", "aws-s3", "minio", "cloudflare-r2"] as const).map(
              (value) => (
                <option key={value} value={value}>
                  {t(`providers.${value}`)}
                </option>
              ),
            )}
          </select>
        </label>
        {provider === "cloudflare-r2" && (
          <label className="field">
            <span>{t("fields.accountId")}</span>
            <input
              defaultValue={profile?.accountId}
              name="accountId"
              required
            />
          </label>
        )}
        <label className="field">
          <span>{t("fields.bucket")}</span>
          <input defaultValue={profile?.bucket} name="bucket" required />
        </label>
        {provider !== "cloudflare-r2" && (
          <label className="field">
            <span>{t("fields.region")}</span>
            <input
              defaultValue={
                profile?.region ?? (provider === "minio" ? "us-east-1" : "")
              }
              name="region"
              required={provider !== "minio"}
            />
          </label>
        )}
        {(provider === "minio" || provider === "aliyun-oss") && (
          <label className="field field-wide">
            <span>{t("fields.endpoint")}</span>
            <input
              defaultValue={profile?.endpoint}
              name="endpoint"
              placeholder={
                provider === "minio"
                  ? "https://minio.example.com"
                  : t("editor.optional")
              }
              required={provider === "minio"}
              type="url"
            />
          </label>
        )}
        <label className="field">
          <span>{t("fields.accessKeyId")}</span>
          <input autoComplete="off" name="accessKeyId" required={!profile} />
        </label>
        <label className="field">
          <span>{t("fields.accessKeySecret")}</span>
          <input
            autoComplete="new-password"
            name="accessKeySecret"
            required={!profile}
            type="password"
          />
        </label>
      </div>
      {profile && <p className="help-text">{t("editor.secretHint")}</p>}
      {testPassed && (
        <p className="storage-test-ok">
          <CheckCircle2 size={16} />
          {t("editor.testPassed")}
        </p>
      )}
      <div className="management-editor__actions">
        <button className="button secondary" onClick={onClose} type="button">
          {t("actions.cancel")}
        </button>
        {canTest && (
          <button
            className="button secondary"
            disabled={testing || saving}
            onClick={(event) => {
              if (event.currentTarget.form)
                submit(event.currentTarget.form, "test");
            }}
            type="button"
          >
            {testing ? t("actions.testing") : t("actions.test")}
          </button>
        )}
        <button className="button" disabled={saving || testing} type="submit">
          {saving ? t("actions.saving") : t("actions.save")}
        </button>
      </div>
    </form>
  );
}

function PageState({
  text,
  error = false,
  action,
}: {
  text: string;
  error?: boolean;
  action?: React.ReactNode;
}) {
  return (
    <div
      className={error ? "management-error panel" : "empty-state panel"}
      role={error ? "alert" : "status"}
    >
      <p>{text}</p>
      {action}
    </div>
  );
}
