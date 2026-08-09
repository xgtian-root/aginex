"use client";

import { useMutation, useQuery } from "@tanstack/react-query";
import {
  ArrowRight,
  Check,
  ChevronLeft,
  CircleAlert,
  Database,
  Eye,
  EyeOff,
  LoaderCircle,
  LockKeyhole,
  RotateCw,
  ServerCog,
  ShieldCheck,
  UserRound,
} from "lucide-react";
import { useTranslations } from "next-intl";
import {
  type FormEvent,
  type ReactNode,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { LocaleSwitcher } from "@/components/locale-switcher";
import {
  ApiError,
  completeSetup,
  getSetupStatus,
  getSystemMode,
  type SetupComplete,
  type SetupDatabaseTest,
  type SetupStatus,
  testSetupDatabase,
} from "@/lib/api";
import { localizeApiError } from "@/lib/problem-message";

type DatabaseInput = SetupDatabaseTest["database"];
type AdministratorInput = SetupComplete["administrator"];
type WizardStep = 1 | 2 | 3;

const databaseDefaults: Record<DatabaseInput["driver"], string> = {
  sqlite: "data/aginex.db",
  postgres:
    "postgres://aginex:password@database.example:5432/aginex?sslmode=require",
  mysql: "aginex:password@tcp(database.example:3306)/aginex?parseTime=true",
};

const databaseDrivers = [
  "sqlite",
  "postgres",
  "mysql",
] as const satisfies ReadonlyArray<DatabaseInput["driver"]>;

const initializationStages = [
  "validating_database",
  "migrating",
  "bootstrapping",
  "starting_application",
  "persisting_configuration",
  "activating_application",
] as const satisfies ReadonlyArray<SetupStatus["stage"]>;

export function SetupWizard() {
  const t = useTranslations("Setup");
  const translate = useTranslations();
  const [step, setStep] = useState<WizardStep>(1);
  const [database, setDatabase] = useState<DatabaseInput>({
    driver: "sqlite",
    dsn: databaseDefaults.sqlite,
  });
  const [administrator, setAdministrator] = useState<AdministratorInput>({
    email: "",
    password: "",
  });
  const [confirmation, setConfirmation] = useState("");
  const [testedFingerprint, setTestedFingerprint] = useState("");
  const [showDSN, setShowDSN] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [adminError, setAdminError] = useState("");
  const headingRef = useRef<HTMLHeadingElement>(null);

  const normalizedDatabase = useMemo<DatabaseInput>(
    () => ({ ...database, dsn: database.dsn.trim() }),
    [database],
  );
  const databaseFingerprint = JSON.stringify(normalizedDatabase);

  const mode = useQuery({
    queryKey: ["system-mode"],
    queryFn: getSystemMode,
    retry: false,
    staleTime: 0,
    gcTime: 0,
    refetchOnWindowFocus: "always",
    refetchInterval: step === 3 ? 1_250 : false,
  });
  const setupStatus = useQuery({
    queryKey: ["setup-status"],
    queryFn: getSetupStatus,
    enabled: mode.data?.mode === "setup",
    retry: false,
    staleTime: 0,
    gcTime: 0,
    refetchOnWindowFocus: "always",
    refetchInterval: (query) =>
      query.state.data?.status === "initializing" ? 1_250 : false,
  });

  const databaseTest = useMutation({
    mutationFn: testSetupDatabase,
    onSuccess: (_result, request) =>
      setTestedFingerprint(JSON.stringify(request.database)),
  });
  const initialization = useMutation({
    mutationFn: completeSetup,
    onSettled: async () => {
      await Promise.all([mode.refetch(), setupStatus.refetch()]);
    },
  });

  useEffect(() => {
    if (mode.data?.mode === "application") {
      window.location.replace("/login");
    }
  }, [mode.data?.mode]);

  useEffect(() => {
    if (
      setupStatus.data?.status === "initializing" ||
      setupStatus.data?.status === "failed"
    ) {
      setStep(3);
    }
  }, [setupStatus.data?.status]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: moving between wizard steps must move keyboard focus to the new heading.
  useEffect(() => {
    headingRef.current?.focus();
  }, [step]);

  useEffect(() => {
    const verifyRestoredPage = (event: PageTransitionEvent) => {
      if (event.persisted) void mode.refetch();
    };
    const verifyFocusedPage = () => void mode.refetch();
    window.addEventListener("pageshow", verifyRestoredPage);
    window.addEventListener("focus", verifyFocusedPage);
    return () => {
      window.removeEventListener("pageshow", verifyRestoredPage);
      window.removeEventListener("focus", verifyFocusedPage);
    };
  }, [mode]);

  if (mode.isPending) return <SetupProbeLoading />;
  if (mode.isError) {
    return (
      <SetupClientUnavailable
        detail={localizeApiError(
          mode.error,
          translate,
          t("probe.defaultUnavailableDetail"),
          { REQUEST_FAILED: t("probe.defaultUnavailableDetail") },
        )}
        onRetry={() => mode.refetch()}
        pending={mode.isFetching}
      />
    );
  }
  if (mode.data?.mode === "application") return <SetupProbeLoading />;

  if (setupStatus.isPending) return <SetupProbeLoading />;
  if (setupStatus.isError) {
    return (
      <SetupClientUnavailable
        detail={localizeApiError(
          setupStatus.error,
          translate,
          t("probe.supervisorStateFallback"),
          { REQUEST_FAILED: t("probe.supervisorStateFallback") },
        )}
        onRetry={() => Promise.all([mode.refetch(), setupStatus.refetch()])}
        pending={setupStatus.isFetching || mode.isFetching}
      />
    );
  }

  const tested = testedFingerprint === databaseFingerprint;
  const currentStage = setupStatus.data?.stage ?? "waiting";

  function updateDriver(driver: DatabaseInput["driver"]) {
    setDatabase({ driver, dsn: databaseDefaults[driver] });
    setTestedFingerprint("");
    databaseTest.reset();
  }

  function updateDSN(dsn: string) {
    setDatabase((current: DatabaseInput) => ({ ...current, dsn }));
    setTestedFingerprint("");
    databaseTest.reset();
  }

  function testConnection(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (databaseTest.isPending) return;
    setDatabase(normalizedDatabase);
    databaseTest.mutate({ database: normalizedDatabase });
  }

  function reviewAdministrator(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setAdminError("");
    if (!tested) {
      setStep(1);
      return;
    }
    if (administrator.password !== confirmation) {
      setAdminError(t("administrator.passwordMismatch"));
      return;
    }
    const request: SetupComplete = {
      database: normalizedDatabase,
      administrator: {
        email: administrator.email.trim().toLowerCase(),
        password: administrator.password,
      },
    };
    setAdministrator(request.administrator);
    setStep(3);
    initialization.mutate(request);
  }

  function retryInitialization() {
    initialization.reset();
    initialization.mutate({
      database: normalizedDatabase,
      administrator,
    });
  }

  function reviewConfiguration() {
    initialization.reset();
    setTestedFingerprint("");
    setStep(1);
  }

  return (
    <main className="setup-page" data-step={step}>
      <a className="skip-link" href="#setup-content">
        {t("skipLink")}
      </a>
      <SetupLedger step={step} />
      <div className="setup-workbench" id="setup-content">
        <header className="setup-header">
          <div>
            <span className="setup-brand-mark" aria-hidden>
              A
            </span>
            <span className="setup-wordmark">{t("header.wordmark")}</span>
          </div>
          <div>
            <span className="setup-serial">{t("header.serial")}</span>
            <LocaleSwitcher />
          </div>
        </header>

        <section className="setup-sheet" aria-live="polite">
          {step === 1 && (
            <DatabaseStep
              database={database}
              error={
                databaseTest.isError
                  ? localizeApiError(
                      databaseTest.error,
                      translate,
                      t("database.verificationFallback"),
                      { REQUEST_FAILED: t("database.verificationFallback") },
                    )
                  : ""
              }
              headingRef={headingRef}
              onContinue={() => setStep(2)}
              onDriverChange={updateDriver}
              onDSNChange={updateDSN}
              onSubmit={testConnection}
              onToggleDSN={() => setShowDSN((visible) => !visible)}
              pending={databaseTest.isPending}
              showDSN={showDSN}
              tested={tested}
            />
          )}
          {step === 2 && (
            <AdministratorStep
              administrator={administrator}
              confirmation={confirmation}
              error={adminError}
              headingRef={headingRef}
              onAdministratorChange={setAdministrator}
              onBack={() => setStep(1)}
              onConfirmationChange={setConfirmation}
              onSubmit={reviewAdministrator}
              onTogglePassword={() => setShowPassword((visible) => !visible)}
              pending={initialization.isPending}
              showPassword={showPassword}
            />
          )}
          {step === 3 && (
            <InitializationStep
              canRetry={
                tested &&
                administrator.email.trim().length > 0 &&
                administrator.password.length >= 12
              }
              error={
                setupStatus.data?.status === "failed"
                  ? setupStatus.data.code
                    ? t("initialization.failureWithReference", {
                        code: setupStatus.data.code,
                      })
                    : t("initialization.failure")
                  : setupStatus.data?.status !== "initializing" &&
                      initialization.isError &&
                      !isSetupInProgress(initialization.error)
                    ? localizeApiError(
                        initialization.error,
                        translate,
                        t("initialization.requestFallback"),
                        {
                          REQUEST_FAILED: t("initialization.requestFallback"),
                        },
                      )
                    : ""
              }
              headingRef={headingRef}
              onRetry={retryInitialization}
              onReview={reviewConfiguration}
              pending={
                initialization.isPending ||
                setupStatus.data?.status === "initializing"
              }
              stage={currentStage}
            />
          )}
        </section>
      </div>
    </main>
  );
}

function SetupLedger({ step }: { step: WizardStep }) {
  const t = useTranslations("Setup");
  const items: Array<{
    index: WizardStep;
    title: string;
    caption: string;
    icon: typeof Database;
  }> = [
    {
      index: 1,
      title: t("ledger.steps.database.title"),
      caption: t("ledger.steps.database.caption"),
      icon: Database,
    },
    {
      index: 2,
      title: t("ledger.steps.administrator.title"),
      caption: t("ledger.steps.administrator.caption"),
      icon: UserRound,
    },
    {
      index: 3,
      title: t("ledger.steps.initialize.title"),
      caption: t("ledger.steps.initialize.caption"),
      icon: ServerCog,
    },
  ];

  return (
    <aside className="setup-ledger" aria-label={t("ledger.progressAriaLabel")}>
      <div className="setup-ledger__intro">
        <p className="eyebrow">{t("ledger.eyebrow")}</p>
        <h1>{t("ledger.title")}</h1>
        <p>{t("ledger.description")}</p>
      </div>
      <ol className="setup-steps">
        {items.map(({ index, title, caption, icon: Icon }) => {
          const state =
            index < step ? "complete" : index === step ? "active" : "next";
          return (
            <li
              aria-current={index === step ? "step" : undefined}
              className={`setup-step setup-step--${state}`}
              key={index}
            >
              <span className="setup-step__number" aria-hidden>
                {index < step ? <Check size={15} /> : `0${index}`}
              </span>
              <Icon aria-hidden size={18} strokeWidth={1.65} />
              <span>
                <strong>{title}</strong>
                <small>{caption}</small>
              </span>
              {index === step && (
                <span className="sr-only">{t("ledger.currentStep")}</span>
              )}
            </li>
          );
        })}
      </ol>
      <p className="setup-ledger__note">{t("ledger.note")}</p>
    </aside>
  );
}

function DatabaseStep({
  database,
  error,
  headingRef,
  onContinue,
  onDriverChange,
  onDSNChange,
  onSubmit,
  onToggleDSN,
  pending,
  showDSN,
  tested,
}: {
  database: DatabaseInput;
  error: string;
  headingRef: React.RefObject<HTMLHeadingElement | null>;
  onContinue: () => void;
  onDriverChange: (driver: DatabaseInput["driver"]) => void;
  onDSNChange: (dsn: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onToggleDSN: () => void;
  pending: boolean;
  showDSN: boolean;
  tested: boolean;
}) {
  const t = useTranslations("Setup");
  return (
    <section aria-labelledby="setup-step-01-title" className="setup-step-panel">
      <StepHeading
        caption={t("database.heading.caption")}
        headingRef={headingRef}
        index="01"
        title={t("database.heading.title")}
      >
        {t("database.heading.description")}
      </StepHeading>

      <form aria-busy={pending} className="setup-form" onSubmit={onSubmit}>
        <fieldset className="driver-fieldset">
          <legend>{t("database.engineLabel")}</legend>
          <div className="driver-options">
            {databaseDrivers.map((driver) => (
              <label key={driver}>
                <input
                  checked={database.driver === driver}
                  disabled={pending}
                  name="driver"
                  onChange={() => onDriverChange(driver)}
                  type="radio"
                  value={driver}
                />
                <span>{t(`database.drivers.${driver}`)}</span>
              </label>
            ))}
          </div>
          <p className="field-hint">{t("database.productionHint")}</p>
        </fieldset>

        <div className="field setup-field">
          <div className="setup-field__label">
            <label htmlFor="database-dsn">
              {database.driver === "sqlite"
                ? t("database.pathLabel")
                : t("database.connectionStringLabel")}
            </label>
            <span>{t("database.required")}</span>
          </div>
          <div className="secret-input">
            <input
              aria-describedby={
                error ? "database-dsn-hint database-error" : "database-dsn-hint"
              }
              aria-invalid={Boolean(error)}
              autoComplete="off"
              className="input"
              id="database-dsn"
              maxLength={8192}
              name="dsn"
              onChange={(event) => onDSNChange(event.target.value)}
              disabled={pending}
              required
              spellCheck={false}
              type={
                database.driver === "sqlite" || showDSN ? "text" : "password"
              }
              value={database.dsn}
            />
            {database.driver !== "sqlite" && (
              <button
                aria-label={
                  showDSN
                    ? t("database.hideConnectionString")
                    : t("database.showConnectionString")
                }
                aria-pressed={showDSN}
                className="secret-toggle"
                onClick={onToggleDSN}
                type="button"
              >
                {showDSN ? (
                  <EyeOff aria-hidden size={17} />
                ) : (
                  <Eye aria-hidden size={17} />
                )}
              </button>
            )}
          </div>
          <p className="field-hint" id="database-dsn-hint">
            {database.driver === "sqlite"
              ? t("database.sqliteHint")
              : t("database.serverHint")}
          </p>
        </div>

        {error && <InlineError id="database-error">{error}</InlineError>}
        {tested && (
          <div className="connection-proof" role="status">
            <ShieldCheck aria-hidden size={19} />
            <span>
              <strong>{t("database.connectionVerified")}</strong>
              <small>{t("database.connectionVerifiedDescription")}</small>
            </span>
          </div>
        )}

        <div className="setup-actions">
          <button
            className="button setup-test-button"
            disabled={pending}
            type="submit"
          >
            {pending ? (
              <>
                <LoaderCircle className="spin" aria-hidden size={17} />
                {t("database.testing")}
              </>
            ) : (
              <>
                <Database aria-hidden size={17} />
                {tested
                  ? t("database.testAgain")
                  : t("database.testConnection")}
              </>
            )}
          </button>
          <button
            className="button setup-continue-button"
            disabled={!tested || pending}
            onClick={onContinue}
            type="button"
          >
            {t("database.continue")}
            <ArrowRight aria-hidden size={17} />
          </button>
        </div>
      </form>
    </section>
  );
}

function AdministratorStep({
  administrator,
  confirmation,
  error,
  headingRef,
  onAdministratorChange,
  onBack,
  onConfirmationChange,
  onSubmit,
  onTogglePassword,
  pending,
  showPassword,
}: {
  administrator: AdministratorInput;
  confirmation: string;
  error: string;
  headingRef: React.RefObject<HTMLHeadingElement | null>;
  onAdministratorChange: (value: AdministratorInput) => void;
  onBack: () => void;
  onConfirmationChange: (value: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onTogglePassword: () => void;
  pending: boolean;
  showPassword: boolean;
}) {
  const t = useTranslations("Setup");
  return (
    <section aria-labelledby="setup-step-02-title" className="setup-step-panel">
      <StepHeading
        caption={t("administrator.heading.caption")}
        headingRef={headingRef}
        index="02"
        title={t("administrator.heading.title")}
      >
        {t("administrator.heading.description")}
      </StepHeading>

      <form aria-busy={pending} className="setup-form" onSubmit={onSubmit}>
        <div className="field setup-field">
          <div className="setup-field__label">
            <label htmlFor="administrator-email">
              {t("administrator.emailLabel")}
            </label>
            <span>{t("administrator.identifierHint")}</span>
          </div>
          <input
            autoComplete="email"
            className="input"
            id="administrator-email"
            maxLength={320}
            name="email"
            onChange={(event) =>
              onAdministratorChange({
                ...administrator,
                email: event.target.value,
              })
            }
            placeholder={t("administrator.emailPlaceholder")}
            required
            type="email"
            value={administrator.email}
          />
        </div>

        <div className="setup-password-grid">
          <div className="field setup-field">
            <div className="setup-field__label">
              <label htmlFor="administrator-password">
                {t("administrator.passwordLabel")}
              </label>
              <span>{t("administrator.passwordLengthHint")}</span>
            </div>
            <div className="secret-input">
              <input
                autoComplete="new-password"
                className="input"
                id="administrator-password"
                maxLength={1024}
                minLength={12}
                name="password"
                onChange={(event) =>
                  onAdministratorChange({
                    ...administrator,
                    password: event.target.value,
                  })
                }
                required
                type={showPassword ? "text" : "password"}
                value={administrator.password}
              />
              <button
                aria-label={
                  showPassword
                    ? t("administrator.hidePassword")
                    : t("administrator.showPassword")
                }
                aria-pressed={showPassword}
                className="secret-toggle"
                onClick={onTogglePassword}
                type="button"
              >
                {showPassword ? (
                  <EyeOff aria-hidden size={17} />
                ) : (
                  <Eye aria-hidden size={17} />
                )}
              </button>
            </div>
          </div>
          <div className="field setup-field">
            <div className="setup-field__label">
              <label htmlFor="administrator-password-confirmation">
                {t("administrator.confirmPasswordLabel")}
              </label>
              <span>{t("administrator.exactMatchHint")}</span>
            </div>
            <input
              aria-describedby={error ? "administrator-error" : undefined}
              aria-invalid={Boolean(error)}
              autoComplete="new-password"
              className="input"
              id="administrator-password-confirmation"
              maxLength={1024}
              minLength={12}
              name="password-confirmation"
              onChange={(event) => onConfirmationChange(event.target.value)}
              required
              type={showPassword ? "text" : "password"}
              value={confirmation}
            />
          </div>
        </div>

        <div className="credential-note">
          <LockKeyhole aria-hidden size={18} />
          <p>{t("administrator.credentialNote")}</p>
        </div>
        {error && <InlineError id="administrator-error">{error}</InlineError>}

        <div className="setup-actions setup-actions--split">
          <button
            className="button setup-back-button"
            onClick={onBack}
            type="button"
          >
            <ChevronLeft aria-hidden size={17} />
            {t("administrator.backToDatabase")}
          </button>
          <button
            className="button setup-continue-button"
            disabled={pending}
            type="submit"
          >
            {t("administrator.initializeWorkspace")}
            <ArrowRight aria-hidden size={17} />
          </button>
        </div>
      </form>
    </section>
  );
}

function InitializationStep({
  canRetry,
  error,
  headingRef,
  onRetry,
  onReview,
  pending,
  stage,
}: {
  canRetry: boolean;
  error: string;
  headingRef: React.RefObject<HTMLHeadingElement | null>;
  onRetry: () => void;
  onReview: () => void;
  pending: boolean;
  stage: SetupStatus["stage"];
}) {
  const t = useTranslations("Setup");
  const currentIndex = initializationStages.indexOf(
    stage as (typeof initializationStages)[number],
  );
  const currentStageLabel = t(`initialization.stages.${stage}`);

  return (
    <section
      aria-labelledby="setup-step-03-title"
      className="setup-step-panel setup-step-panel--initializing"
    >
      <StepHeading
        caption={
          error
            ? t("initialization.pausedCaption")
            : t("initialization.runningCaption")
        }
        headingRef={headingRef}
        index="03"
        title={
          error
            ? t("initialization.pausedTitle")
            : t("initialization.runningTitle")
        }
      >
        {error
          ? t("initialization.pausedDescription")
          : t("initialization.runningDescription")}
      </StepHeading>

      <div className="initialization-layout">
        <ol
          className="initialization-stages"
          aria-label={t("initialization.progressAriaLabel")}
          aria-busy={pending}
        >
          {initializationStages.map((item, index) => {
            const complete = currentIndex > index;
            const active =
              stage === item || (stage === "waiting" && index === 0);
            return (
              <li
                aria-current={active ? "step" : undefined}
                className={
                  complete ? "complete" : active ? "active" : "pending"
                }
                data-state={
                  complete ? "complete" : active ? "active" : "pending"
                }
                key={item}
              >
                <span aria-hidden>
                  {complete ? (
                    <Check size={14} />
                  ) : active && pending ? (
                    <LoaderCircle className="spin" size={14} />
                  ) : (
                    String(index + 1).padStart(2, "0")
                  )}
                </span>
                <strong>{t(`initialization.stages.${item}`)}</strong>
              </li>
            );
          })}
        </ol>

        <div
          className={
            error ? "initialization-signal error" : "initialization-signal"
          }
          data-state={error ? "error" : "running"}
          role={error ? "alert" : "status"}
        >
          {error ? (
            <CircleAlert aria-hidden size={24} />
          ) : (
            <ServerCog aria-hidden size={24} />
          )}
          <p className="eyebrow">
            {error
              ? t("initialization.actionRequired")
              : t("initialization.automaticHandoff")}
          </p>
          <strong>{error || currentStageLabel}</strong>
          <small>
            {error
              ? t("initialization.errorDescription")
              : t("initialization.runningStatusDescription")}
          </small>
        </div>
      </div>

      {error && (
        <div className="setup-actions setup-actions--split">
          <button
            className="button setup-back-button"
            disabled={pending}
            onClick={onReview}
            type="button"
          >
            <ChevronLeft aria-hidden size={17} />
            {t("initialization.reviewConfiguration")}
          </button>
          {canRetry && (
            <button
              className="button setup-continue-button"
              disabled={pending}
              onClick={onRetry}
              type="button"
            >
              <RotateCw aria-hidden size={16} />
              {t("initialization.retryInitialization")}
            </button>
          )}
        </div>
      )}
    </section>
  );
}

function StepHeading({
  caption,
  children,
  headingRef,
  index,
  title,
}: {
  caption: string;
  children: ReactNode;
  headingRef: React.RefObject<HTMLHeadingElement | null>;
  index: string;
  title: string;
}) {
  return (
    <header className="setup-step-heading">
      <div className="setup-step-heading__index" aria-hidden>
        {index}
      </div>
      <div>
        <p className="eyebrow">{caption}</p>
        <h2 id={`setup-step-${index}-title`} ref={headingRef} tabIndex={-1}>
          {title}
        </h2>
        <p>{children}</p>
      </div>
    </header>
  );
}

function InlineError({ children, id }: { children: ReactNode; id?: string }) {
  return (
    <div className="setup-inline-error" id={id} role="alert">
      <CircleAlert aria-hidden size={18} />
      <p>{children}</p>
    </div>
  );
}

function SetupProbeLoading() {
  const t = useTranslations("Setup");
  return (
    <main className="setup-probe" aria-live="polite" aria-busy="true">
      <LocaleSwitcher />
      <div className="setup-probe__instrument" aria-hidden>
        <span />
        <LoaderCircle className="spin" size={22} />
      </div>
      <p className="eyebrow">{t("probe.eyebrow")}</p>
      <h1>{t("probe.title")}</h1>
      <p>{t("probe.description")}</p>
    </main>
  );
}

function SetupClientUnavailable({
  detail,
  onRetry,
  pending,
}: {
  detail?: string;
  onRetry: () => unknown;
  pending: boolean;
}) {
  const t = useTranslations("Setup");
  return (
    <main className="setup-probe setup-probe--error" role="alert">
      <LocaleSwitcher />
      <CircleAlert aria-hidden size={26} />
      <p className="eyebrow">{t("probe.unavailableEyebrow")}</p>
      <h1>{t("probe.unavailableTitle")}</h1>
      <p>{detail ?? t("probe.defaultUnavailableDetail")}</p>
      <button
        className="button"
        disabled={pending}
        onClick={onRetry}
        type="button"
      >
        {pending ? (
          <LoaderCircle className="spin" aria-hidden size={16} />
        ) : (
          <RotateCw aria-hidden size={16} />
        )}
        {t("probe.action")}
      </button>
    </main>
  );
}

function isSetupInProgress(error: unknown) {
  return (
    error instanceof ApiError &&
    error.response.status === 409 &&
    error.problem.code === "SETUP_IN_PROGRESS"
  );
}
