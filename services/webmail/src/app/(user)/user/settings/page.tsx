"use client";

import React, { useEffect, useRef, useState } from "react";
import {
  Shield,
  Filter,
  Palmtree,
  FileText,
  KeyRound,
  Monitor,
  CheckCircle2,
  Trash2,
  ChevronUp,
  ChevronDown,
  Plus,
  Lock,
  Languages,
} from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { Badge } from "../../../../components/ui/Badge";
import { Button } from "../../../../components/ui/Button";
import { TotpEnrolment, RecoveryCodes } from "../../../../components/TotpEnrolment";
import { LanguageSwitcher } from "../../../../i18n/LanguageSwitcher";
import { signatureToHtml, sanitizeSignature } from "../../../../lib/signature";
import { buildSieve, parseSieve, type Rule, type RuleField, type RuleCondition, type RuleAction } from "../../../../lib/rules";
import { useFolders } from "../../../../lib/useFolders";
import { moveTargets } from "../../../../lib/mailCounts";

interface SieveRule {
  id: string;
  field: "From" | "Subject" | "Header";
  match: "contains" | "equals" | "matches";
  value: string;
  action: "move" | "flag" | "discard" | "redirect";
  targetFolder?: string;
}

interface AppPassword {
  id: string;
  label: string;
  created_at?: string;
  last_used_at?: string | null;
}

interface WebSession {
  id: string;
  surface: string;
  ip_address: string | null;
  user_agent: string | null;
  created_at: string;
  current: boolean;
}

type SettingsTab = "security" | "filters" | "vacation" | "signature" | "language";

export default function UserSettingsPage() {
  const t = useTranslations();
  const [activeTab, setActiveTab] = useState<SettingsTab>("security");

  const [mfaEnabled, setMfaEnabled] = useState(false);
  const [qrSecret, setQrSecret] = useState<string | null>(null);
  const [qrUri, setQrUri] = useState<string | null>(null);
  const [totpCode, setTotpCode] = useState("");
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  const [appPasswords, setAppPasswords] = useState<AppPassword[]>([]);
  const [newAppPassLabel, setNewAppPassLabel] = useState("");
  const [generatedAppPass, setGeneratedAppPass] = useState<string | null>(null);
  const [sessions, setSessions] = useState<WebSession[]>([]);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");

  const [vacationEnabled, setVacationEnabled] = useState(false);
  const [vacationSubject, setVacationSubject] = useState(() => t("settings.vacationDefaultSubject"));
  const [vacationBody, setVacationBody] = useState(() => t("settings.vacationDefaultBody"));
  const [vacationSaved, setVacationSaved] = useState(false);

  const [signature, setSignature] = useState("");
  const signatureRef = useRef<HTMLDivElement>(null);

  // execCommand edits the DOM directly, so the mirrored value is re-read here
  // rather than relying on an input event that may not be dispatched.
  const applySignatureCommand = (command: string, value?: string) => {
    signatureRef.current?.focus();
    document.execCommand(command, false, value);
    setSignature(signatureRef.current?.innerHTML ?? "");
  };
  const [sigSaved, setSigSaved] = useState(false);

  // Starts empty rather than with a sample rule. A seeded "[Boletim]" filter
  // used to sit here, and because saving posts the whole list, the first time
  // anyone added or removed a rule that invented filter was written into their
  // real Sieve script and began moving their mail.
  const { folders } = useFolders();
  const [rules, setRules] = useState<Rule[]>([]);
  // A script this screen cannot express - hand-written, or written by a
  // desktop client over ManageSieve. Shown as it is rather than rewritten into
  // the nearest thing this form can say, which would quietly change what the
  // user's mail does.
  const [foreignScript, setForeignScript] = useState<string | null>(null);
  const [newField, setNewField] = useState<RuleField>("subject");
  const [newCondition, setNewCondition] = useState<RuleCondition>("contains");
  const [newAction, setNewAction] = useState<RuleAction>("move");
  const [newValue, setNewValue] = useState("");
  const [newTarget, setNewTarget] = useState("Archive");

  useEffect(() => {
    fetch("/api/v1/user/preferences")
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => {
        if (data?.signature) {
          const html = signatureToHtml(data.signature);
          setSignature(html);
          if (signatureRef.current) signatureRef.current.innerHTML = html;
        }
      })
      .catch(() => undefined);

    fetch("/api/v1/user/sieve")
      .then((res) => (res.ok ? res.json() : null))
      .then((data) => {
        const script = typeof data?.script === "string" ? data.script : "";
        const parsed = parseSieve(script);
        if (parsed === null) {
          // Not expressible in this form; shown verbatim instead of being
          // rewritten into something simpler that means something else.
          setForeignScript(script);
          setRules([]);
        } else {
          setForeignScript(null);
          setRules(parsed);
        }
      })
      .catch(() => undefined);
  }, []);

  const saveSignature = async () => {
    try {
      await fetch("/api/v1/user/preferences", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ signature: sanitizeSignature(signature), auto_save_drafts: true }),
      });
      localStorage.setItem("lm_user_signature", sanitizeSignature(signature));
      setSigSaved(true);
      setTimeout(() => setSigSaved(false), 2000);
    } catch {
      setMessage(t("settings.signatureSaveFailed"));
    }
  };

  const saveVacationSettings = async (enabled: boolean, subject: string, body: string) => {
    setVacationEnabled(enabled);
    try {
      await fetch("/api/v1/user/vacation", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled, subject, message: body }),
      });
      setVacationSaved(true);
      setTimeout(() => setVacationSaved(false), 2000);
    } catch {
      setMessage(t("settings.autoReplySaveFailed"));
    }
  };

  /** Writes the whole rule list to the server as one script. */
  const persistRules = async (updated: Rule[]) => {
    setRules(updated);
    await fetch("/api/v1/user/sieve", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "user-rules", script: buildSieve(updated), is_active: true }),
    }).catch(() => undefined);
  };

  const addSieveRule = async (e: React.FormEvent) => {
    e.preventDefault();
    const value = newValue.trim();
    if (!value) return;
    await persistRules([
      ...rules,
      {
        id: Date.now().toString(),
        field: newField,
        condition: newCondition,
        value,
        action: newAction,
        target: newTarget,
      },
    ]);
    setNewValue("");
  };

  const removeSieveRule = async (id: string) => {
    await persistRules(rules.filter((r) => r.id !== id));
  };

  /** Moves a rule, since the first one that files a message decides. */
  const moveRule = async (index: number, delta: number) => {
    const target = index + delta;
    if (target < 0 || target >= rules.length) return;
    const reordered = [...rules];
    const [moved] = reordered.splice(index, 1);
    reordered.splice(target, 0, moved);
    await persistRules(reordered);
  };

  const saveVacationResponder = (e: React.FormEvent) => {
    e.preventDefault();
    void saveVacationSettings(vacationEnabled, vacationSubject, vacationBody);
  };

  const loadAccount = async () => {
    try {
      const [me, passwords, activeSessions] = await Promise.all([
        fetch("/api/v1/user/me").then((r) => (r.ok ? r.json() : null)),
        fetch("/api/v1/user/app-passwords").then((r) => (r.ok ? r.json() : [])),
        fetch("/api/v1/user/sessions").then((r) => (r.ok ? r.json() : [])),
      ]);
      if (me) setMfaEnabled(Boolean(me.mfa_enrolled));
      setAppPasswords(passwords);
      setSessions(activeSessions);
    } catch {
      // ignore
    }
  };

  useEffect(() => {
    void loadAccount();
  }, []);

  const createAppPassword = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newAppPassLabel.trim()) return;
    const res = await fetch("/api/v1/user/app-passwords", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ label: newAppPassLabel.trim() }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      setMessage(data.message ?? t("errors.serverError"));
      return;
    }
    setGeneratedAppPass(data.password);
    setNewAppPassLabel("");
    await loadAccount();
  };

  const revokeAppPassword = async (id: string) => {
    await fetch(`/api/v1/user/app-passwords/${id}`, { method: "DELETE" });
    await loadAccount();
  };

  const revokeSession = async (id: string) => {
    await fetch(`/api/v1/user/sessions/${id}`, { method: "DELETE" });
    await loadAccount();
  };

  const revokeOtherSessions = async () => {
    await fetch("/api/v1/user/sessions/others", { method: "DELETE" });
    await loadAccount();
  };

  const changePassword = async (e: React.FormEvent) => {
    e.preventDefault();
    const res = await fetch("/api/v1/user/password", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
    });
    const data = await res.json().catch(() => ({}));
    setMessage(res.ok ? t("ui.passwordChanged") : (data.message ?? t("errors.serverError")));
    if (res.ok) {
      setCurrentPassword("");
      setNewPassword("");
      await loadAccount();
    }
  };

  /* The screen was one long scroll of eight panels, so the two things people
     come here to do - turn on a second factor, and write a filter - were
     separated by everything else. The same panels now sit behind a tab strip,
     in the order the redesign groups them. */
  const tabs = [
    { id: "security" as const, label: t("settings.security"), icon: Shield },
    { id: "filters" as const, label: t("settings.sieveTitle"), icon: Filter },
    { id: "vacation" as const, label: t("settings.vacationTitle"), icon: Palmtree },
    { id: "signature" as const, label: t("settings.signatureTitle"), icon: FileText },
    { id: "language" as const, label: t("settings.language"), icon: Languages },
  ];

  const input =
    "w-full min-h-[36px] rounded-[10px] bg-dark-card px-3 py-2 text-[13.5px] text-slate-100 placeholder-slate-500 shadow-edge transition-shadow focus:outline-none focus-visible:shadow-edge-accent";
  const fieldLabel = "mb-1.5 block text-xs text-slate-400";
  const panel = "flex flex-col gap-4 rounded-2xl bg-dark-panel p-5 shadow-edge";
  const panelTitle = "text-[17px] font-medium leading-tight text-slate-100";
  const panelIntro = "mt-1 text-[13px] leading-relaxed text-slate-400";
  const tile = "flex flex-wrap items-center gap-3 rounded-xl bg-dark-card px-3.5 py-3 text-[13px] shadow-edge";

  return (
    <div className="flex-1 overflow-y-auto bg-dark-bg px-5 pb-11 pt-7 sm:px-8">
      <div className="mx-auto flex w-full max-w-[900px] flex-col gap-5">
        <div>
          <h1 className="text-[25px] font-medium leading-tight text-slate-100">{t("settings.title")}</h1>
          <p className="mt-1.5 text-[13.5px] text-slate-400">{t("settings.subtitle")}</p>
        </div>

        {/* Tabs wrap rather than scroll, so a longer translation never pushes
            one out of reach. */}
        <div className="lm-tabstrip self-start">
          {tabs.map((tab) => {
            const Icon = tab.icon;
            return (
              <button
                key={tab.id}
                type="button"
                onClick={() => setActiveTab(tab.id)}
                data-active={activeTab === tab.id}
                aria-pressed={activeTab === tab.id}
                className="lm-tab"
              >
                <Icon className="h-[15px] w-[15px] flex-none" />
                <span>{tab.label}</span>
              </button>
            );
          })}
        </div>

        {message && (
          <div className="flex items-center gap-2 rounded-xl bg-dark-card px-4 py-3 text-[12.5px] text-slate-200 shadow-edge">
            <CheckCircle2 className="h-4 w-4 flex-none text-indigo-500" />
            <span>{message}</span>
          </div>
        )}

        {activeTab === "security" && (
          <div className="flex flex-col gap-4">
            {/* 2FA TOTP SECTION */}
            <section className={panel}>
              <div className="flex flex-wrap items-start justify-between gap-4">
                <div className="min-w-[260px] flex-1">
                  <h2 className={panelTitle}>{t("ui.twoFactorSection")}</h2>
                  <p className={panelIntro}>{t("settings.twoFactorIntro")}</p>
                </div>
                <Badge variant={mfaEnabled ? "success" : "warning"} className="flex-none">
                  {mfaEnabled ? t("settings.mfaEnabled") : t("settings.mfaDisabled")}
                </Badge>
              </div>

              {!mfaEnabled && !qrSecret && (
                <Button
                  variant="primary"
                  size="sm"
                  className="self-start"
                  onClick={async () => {
                    const res = await fetch("/api/v1/user/mfa/totp/enroll", { method: "POST" });
                    const data = await res.json();
                    setQrSecret(data.secret);
                    setQrUri(data.uri);
                  }}
                >
                  <span>{t("settings.enableMfa")}</span>
                </Button>
              )}

              {qrSecret && (
                <div className="flex flex-col gap-4 rounded-xl bg-dark-card p-4 text-xs shadow-edge">
                  <TotpEnrolment secret={qrSecret} uri={qrUri} />
                  {/* Enrolment resumes rather than restarting, so this code is the
                      same one already scanned. Anyone who removed the entry from
                      their app needs a way to ask for a different secret. */}
                  <button
                    type="button"
                    onClick={async () => {
                      const res = await fetch("/api/v1/user/mfa/totp/enroll?reset=1", { method: "POST" });
                      const data = await res.json().catch(() => ({}));
                      if (res.ok) {
                        setQrSecret(data.secret);
                        setQrUri(data.uri);
                        setTotpCode("");
                      }
                    }}
                    className="self-start text-[11.5px] text-slate-400 underline underline-offset-4 hover:text-slate-200"
                  >
                    {t("settings.startOverMfa")}
                  </button>
                  <form
                    onSubmit={async (e) => {
                      e.preventDefault();
                      const res = await fetch("/api/v1/user/mfa/totp/confirm", {
                        method: "POST",
                        headers: { "Content-Type": "application/json" },
                        body: JSON.stringify({ secret: qrSecret, code: totpCode }),
                      });
                      const data = await res.json().catch(() => ({}));
                      if (res.ok) {
                        setMfaEnabled(true);
                        setRecoveryCodes(data.recovery_codes ?? []);
                        setQrSecret(null);
                        setTotpCode("");
                      } else {
                        setMessage(data.message ?? t("auth.verificationFailed"));
                      }
                    }}
                    className="flex flex-wrap items-center gap-3"
                  >
                    <input
                      type="text"
                      inputMode="numeric"
                      autoComplete="one-time-code"
                      value={totpCode}
                      onChange={(e) => setTotpCode(e.target.value.replace(/\D/g, "").slice(0, 6))}
                      placeholder={t("settings.sixDigitCode")}
                      maxLength={7}
                      required
                      className="min-h-[36px] w-[150px] rounded-[10px] bg-dark-rail px-3 py-2 text-center font-mono text-[13.5px] tracking-[0.2em] text-slate-100 shadow-edge focus:outline-none focus-visible:shadow-edge-accent"
                    />
                    <Button type="submit" variant="primary" size="sm">
                      {t("common.confirm")}
                    </Button>
                  </form>
                </div>
              )}

              {recoveryCodes && (
                <RecoveryCodes
                  codes={recoveryCodes}
                  onContinue={() => setRecoveryCodes(null)}
                  continueLabel={t("common.continue")}
                />
              )}
            </section>

            {/* APP PASSWORDS */}
            <section className={panel}>
              <div>
                <h2 className={panelTitle}>{t("ui.appPasswordsSection")}</h2>
              </div>

              {generatedAppPass && (
                <div className="flex flex-col gap-2 rounded-xl bg-dark-card p-4 text-xs shadow-edge">
                  <div className="text-slate-100">{t("ui.appPasswordGenerated")}</div>
                  <code className="lm-code block break-all p-2.5 text-sm">{generatedAppPass}</code>
                  <div className="text-[11px] text-amber-400">{t("ui.copyPasswordNow")}</div>
                </div>
              )}

              <form onSubmit={createAppPassword} className="flex flex-wrap gap-2">
                <input
                  type="text"
                  value={newAppPassLabel}
                  onChange={(e) => setNewAppPassLabel(e.target.value)}
                  placeholder={t("settings.appPasswordPlaceholder")}
                  className={`${input} min-w-[220px] flex-1`}
                />
                <Button type="submit" variant="primary" size="md" className="flex-none">
                  {t("settings.generateAppPassword")}
                </Button>
              </form>

              <div className="flex flex-col gap-2">
                {appPasswords.length === 0 ? (
                  <div className="text-[13px] text-slate-400">{t("ui.noAppPasswords")}</div>
                ) : (
                  appPasswords.map((ap) => (
                    <div key={ap.id} className={tile}>
                      <div className="min-w-0 flex-1">
                        <div className="break-words text-[13.5px] text-slate-100">{ap.label}</div>
                        <div className="mt-0.5 text-[11.5px] text-slate-400">
                          {ap.created_at ? new Date(ap.created_at).toLocaleDateString() : ""}
                        </div>
                      </div>
                      <button
                        type="button"
                        onClick={() => void revokeAppPassword(ap.id)}
                        aria-label={t("common.delete")}
                        className="flex h-[30px] w-[30px] flex-none items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-white/[0.07] hover:text-rose-400"
                      >
                        <Trash2 className="h-[15px] w-[15px]" />
                      </button>
                    </div>
                  ))
                )}
              </div>
            </section>

            {/* ACTIVE SESSIONS */}
            <section className={panel}>
              <div className="flex flex-wrap items-center justify-between gap-4">
                <h2 className={panelTitle}>{t("ui.activeSessions")}</h2>
                <Button variant="secondary" size="sm" onClick={() => void revokeOtherSessions()}>
                  {t("ui.signOutOthers")}
                </Button>
              </div>

              <div className="flex flex-col gap-2">
                {sessions.map((session) => (
                  <div key={session.id} className={tile}>
                    <Monitor className="h-5 w-5 flex-none text-slate-400" />
                    <div className="min-w-0 flex-1">
                      <div className="break-all font-mono text-[13px] text-slate-100">
                        {session.ip_address ?? "-"}
                        <span className="ml-2 font-sans text-[10.5px] uppercase tracking-[0.08em] text-slate-400">
                          {session.surface}
                        </span>
                      </div>
                      <div className="mt-0.5 break-all text-[11.5px] text-slate-400">{session.user_agent ?? ""}</div>
                    </div>
                    {session.current ? (
                      <Badge variant="info" className="flex-none">
                        {t("ui.currentSession")}
                      </Badge>
                    ) : (
                      <button
                        type="button"
                        onClick={() => void revokeSession(session.id)}
                        aria-label={t("common.delete")}
                        className="flex h-[30px] w-[30px] flex-none items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-white/[0.07] hover:text-rose-400"
                      >
                        <Trash2 className="h-[15px] w-[15px]" />
                      </button>
                    )}
                  </div>
                ))}
              </div>
            </section>

            {/* PASSWORD CHANGE */}
            <section className={panel}>
              <div className="flex items-center gap-2">
                <Lock className="h-[17px] w-[17px] flex-none text-indigo-500" />
                <h2 className={panelTitle}>{t("ui.changePassword")}</h2>
              </div>
              <form onSubmit={changePassword} className="flex flex-col gap-3">
                <div>
                  <label htmlFor="current-password" className={fieldLabel}>
                    {t("ui.currentPassword")}
                  </label>
                  <input
                    id="current-password"
                    type="password"
                    value={currentPassword}
                    onChange={(e) => setCurrentPassword(e.target.value)}
                    required
                    className={input}
                  />
                </div>
                <div>
                  <label htmlFor="new-password" className={fieldLabel}>
                    {t("ui.newPassword")}
                  </label>
                  <input
                    id="new-password"
                    type="password"
                    value={newPassword}
                    onChange={(e) => setNewPassword(e.target.value)}
                    minLength={12}
                    required
                    className={input}
                  />
                </div>
                <Button type="submit" variant="primary" size="md" className="self-start">
                  {t("common.save")}
                </Button>
              </form>
            </section>
          </div>
        )}

        {/* VISUAL SIEVE RULES BUILDER */}
        {activeTab === "filters" && (
          <div className="flex flex-col gap-4">
            <section className={panel}>
              <div>
                <h2 className={panelTitle}>{t("settings.sieveTitle")}</h2>
                <p className={panelIntro}>{t("settings.sieveIntro")}</p>
              </div>

              {/* The rule reads as a sentence: IF <field> <match> <value> THEN
                  <action>. Each control keeps its own label, so the row still
                  makes sense when it wraps on a narrow window. */}
              {/* The rule reads as a sentence: IF <field> <condition>
                  <value> THEN <action>. The vocabulary is deliberately the
                  user's, not Sieve's: the old form offered ":matches (regex)"
                  and pasted whatever was typed into a script, which asked
                  someone who wanted "mail from the bank goes in Faturas" to
                  write a pattern language. Wildcards are now generated from
                  "starts with" and "ends with"; nobody types one. */}
              <form onSubmit={addSieveRule} className="flex flex-wrap items-end gap-2.5">
                <div className="pb-2.5 text-xs uppercase tracking-[0.08em] text-slate-400">
                  {t("settings.ruleIf")}
                </div>
                <div className="min-w-[140px] flex-1">
                  <label htmlFor="rule-field" className={fieldLabel}>
                    {t("settings.ruleField")}
                  </label>
                  <select
                    id="rule-field"
                    value={newField}
                    onChange={(e) => setNewField(e.target.value as RuleField)}
                    className={input}
                  >
                    <option value="subject">{t("settings.fieldSubject")}</option>
                    <option value="from">{t("settings.fieldFrom")}</option>
                    <option value="to">{t("settings.fieldTo")}</option>
                    <option value="cc">{t("settings.fieldCc")}</option>
                  </select>
                </div>

                <div className="min-w-[160px] flex-1">
                  <label htmlFor="rule-condition" className={fieldLabel}>
                    {t("settings.ruleCondition")}
                  </label>
                  <select
                    id="rule-condition"
                    value={newCondition}
                    onChange={(e) => setNewCondition(e.target.value as RuleCondition)}
                    className={input}
                  >
                    <option value="contains">{t("settings.condContains")}</option>
                    <option value="is">{t("settings.condIs")}</option>
                    <option value="startsWith">{t("settings.condStartsWith")}</option>
                    <option value="endsWith">{t("settings.condEndsWith")}</option>
                    <option value="notContains">{t("settings.condNotContains")}</option>
                  </select>
                </div>

                <div className="min-w-[200px] flex-[2]">
                  <label htmlFor="rule-value" className={fieldLabel}>
                    {t("settings.ruleValue")}
                  </label>
                  <input
                    id="rule-value"
                    type="text"
                    value={newValue}
                    onChange={(e) => setNewValue(e.target.value)}
                    placeholder={t("settings.ruleValuePlaceholder")}
                    required
                    className={input}
                  />
                </div>

                <div className="pb-2.5 text-xs uppercase tracking-[0.08em] text-slate-400">
                  {t("settings.ruleThen")}
                </div>
                <div className="min-w-[170px] flex-1">
                  <label htmlFor="rule-action" className={fieldLabel}>
                    {t("ui.actions")}
                  </label>
                  <select
                    id="rule-action"
                    value={newAction}
                    onChange={(e) => setNewAction(e.target.value as RuleAction)}
                    className={input}
                  >
                    <option value="move">{t("settings.actionMove")}</option>
                    <option value="markRead">{t("settings.actionMarkRead")}</option>
                    <option value="flag">{t("settings.actionFlag")}</option>
                    <option value="delete">{t("settings.actionDiscard")}</option>
                  </select>
                </div>

                {/* Real folders, listed from the mailbox: the old form typed a
                    folder name into the script by hand, so a rule could name a
                    folder that did not exist and simply never fired. */}
                {newAction === "move" && (
                  <div className="min-w-[170px] flex-1">
                    <label htmlFor="rule-target" className={fieldLabel}>
                      {t("mail.moveTo")}
                    </label>
                    <select
                      id="rule-target"
                      value={newTarget}
                      onChange={(e) => setNewTarget(e.target.value)}
                      className={input}
                    >
                      {moveTargets(folders, "").map((folder) => (
                        <option key={folder.name} value={folder.name}>
                          {folder.name}
                        </option>
                      ))}
                    </select>
                  </div>
                )}

                <Button type="submit" variant="primary" size="md" className="flex-none">
                  <Plus className="h-[15px] w-[15px]" />
                  <span>{t("settings.addRule")}</span>
                </Button>
              </form>
            </section>

            {/* A script this form cannot express is shown as it is. Rewriting
                somebody's rules into the nearest thing this vocabulary can say
                would quietly change what their mail does. */}
            {foreignScript !== null && (
              <section className={panel}>
                <h2 className={panelTitle}>{t("settings.foreignScriptTitle")}</h2>
                <p className={panelIntro}>{t("settings.foreignScriptIntro")}</p>
                <pre className="overflow-x-auto rounded-xl bg-dark-panel p-3.5 font-mono text-[12.5px] leading-relaxed text-slate-300 shadow-edge">
                  {foreignScript}
                </pre>
              </section>
            )}

            {rules.length > 0 && (
              <section className={panel}>
                <p className={panelIntro}>{t("settings.ruleOrderNote")}</p>
                <div className="flex flex-col gap-2">
                  {rules.map((r, index) => (
                    <div key={r.id} className={tile}>
                      <span className="flex h-[22px] w-[22px] flex-none items-center justify-center rounded-[7px] bg-indigo-900 text-[11px] text-indigo-300">
                        {index + 1}
                      </span>
                      {/* Read back as the sentence it was written as, not as
                          the Sieve it became. */}
                      <span className="min-w-[200px] flex-1 break-words text-[13px] leading-relaxed text-slate-200">
                        {t("settings.ruleIf")}{" "}
                        <strong className="font-medium">{t(`settings.field${r.field.charAt(0).toUpperCase()}${r.field.slice(1)}`)}</strong>{" "}
                        {t(`settings.cond${r.condition.charAt(0).toUpperCase()}${r.condition.slice(1)}`).toLowerCase()}{" "}
                        <strong className="font-medium">&quot;{r.value}&quot;</strong>
                        {" \u2192 "}
                        {r.action === "move"
                          ? `${t("mail.moveTo")} ${r.target}`
                          : t(`settings.action${r.action === "delete" ? "Discard" : r.action.charAt(0).toUpperCase() + r.action.slice(1)}`)}
                      </span>
                      {/* Order matters: the first rule that files a message
                          decides where it lands, so the list has to be
                          rearrangeable. */}
                      <button
                        type="button"
                        onClick={() => void moveRule(index, -1)}
                        disabled={index === 0}
                        aria-label={t("settings.ruleMoveUp")}
                        title={t("settings.ruleMoveUp")}
                        className="flex h-[30px] w-[30px] flex-none items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-white/[0.07] hover:text-slate-100 disabled:opacity-30"
                      >
                        <ChevronUp className="h-[15px] w-[15px]" />
                      </button>
                      <button
                        type="button"
                        onClick={() => void moveRule(index, 1)}
                        disabled={index === rules.length - 1}
                        aria-label={t("settings.ruleMoveDown")}
                        title={t("settings.ruleMoveDown")}
                        className="flex h-[30px] w-[30px] flex-none items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-white/[0.07] hover:text-slate-100 disabled:opacity-30"
                      >
                        <ChevronDown className="h-[15px] w-[15px]" />
                      </button>
                      <button
                        type="button"
                        onClick={() => removeSieveRule(r.id)}
                        aria-label={t("common.delete")}
                        className="flex h-[30px] w-[30px] flex-none items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-white/[0.07] hover:text-rose-400"
                      >
                        <Trash2 className="h-[15px] w-[15px]" />
                      </button>
                    </div>
                  ))}
                </div>
              </section>
            )}
          </div>
        )}

        {/* VACATION AUTO-RESPONDER */}
        {activeTab === "vacation" && (
          <section className={panel}>
            <div className="flex flex-wrap items-start justify-between gap-4">
              <div className="min-w-[240px] flex-1">
                <h2 className={panelTitle}>{t("settings.vacationTitle")}</h2>
                <p className={panelIntro}>{t("settings.vacationIntro")}</p>
              </div>
              {/* A segmented pair rather than an unlabelled switch: a toggle
                  with nothing written on it says neither which state it is in
                  nor what turning it on would do. */}
              <div className="flex flex-none gap-[3px] rounded-[10px] bg-dark-card p-[3px] shadow-edge">
                {[
                  { on: true, label: t("ui.active") },
                  { on: false, label: t("settings.mfaDisabled") },
                ].map((option) => (
                  <button
                    key={String(option.on)}
                    type="button"
                    onClick={() => setVacationEnabled(option.on)}
                    aria-pressed={vacationEnabled === option.on}
                    className={`rounded-lg px-3 py-1.5 text-[12.5px] leading-snug transition-colors ${
                      vacationEnabled === option.on
                        ? "text-indigo-500 shadow-edge-accent"
                        : "text-slate-400 hover:text-slate-100"
                    }`}
                  >
                    {option.label}
                  </button>
                ))}
              </div>
            </div>

            {vacationSaved && (
              <div className="flex items-center gap-2 rounded-xl bg-dark-card px-3.5 py-3 text-[12.5px] text-slate-200 shadow-edge">
                <CheckCircle2 className="h-4 w-4 flex-none text-indigo-500" />
                <span>{t("settings.settingsSaved")}</span>
              </div>
            )}

            {vacationEnabled && (
              <form onSubmit={saveVacationResponder} className="flex flex-col gap-3">
                <div>
                  <label htmlFor="vacation-subject" className={fieldLabel}>
                    {t("settings.vacationSubjectLabel")}
                  </label>
                  <input
                    id="vacation-subject"
                    type="text"
                    value={vacationSubject}
                    onChange={(e) => setVacationSubject(e.target.value)}
                    className={input}
                  />
                </div>

                <div>
                  <label htmlFor="vacation-body" className={fieldLabel}>
                    {t("settings.vacationBodyLabel")}
                  </label>
                  <textarea
                    id="vacation-body"
                    rows={5}
                    value={vacationBody}
                    onChange={(e) => setVacationBody(e.target.value)}
                    className={`${input} leading-relaxed`}
                  />
                </div>

                <Button type="submit" variant="primary" size="md" className="self-start">
                  {t("settings.saveAutoReply")}
                </Button>
              </form>
            )}
          </section>
        )}

        {/* EMAIL SIGNATURE MANAGER */}
        {activeTab === "signature" && (
          <div className="flex flex-wrap gap-4">
            <section className={`${panel} min-w-[320px] flex-1`}>
              <div>
                <h2 className={panelTitle}>{t("settings.signatureTitle")}</h2>
                <p className={panelIntro}>{t("settings.signatureIntro")}</p>
              </div>

              {sigSaved && (
                <div className="flex items-center gap-2 rounded-xl bg-dark-card px-3.5 py-3 text-[12.5px] text-slate-200 shadow-edge">
                  <CheckCircle2 className="h-4 w-4 flex-none text-indigo-500" />
                  <span>{t("settings.signatureSaved")}</span>
                </div>
              )}

              <div>
                <label htmlFor="signature-text" className={fieldLabel}>
                  {t("ui.messageBody")}
                </label>
                {/* A rich field rather than a textarea. A signature is a piece
                    of a message - people put a link, a logo and bold type in
                    it - and the plain textarea could hold none of that. It
                    also stored real newlines, which are whitespace once the
                    value is injected into the composer as HTML, so every line
                    break was silently dropped there. */}
                <div className="flex flex-wrap items-center gap-1 rounded-t-xl bg-dark-card px-2 py-1.5 shadow-edge">
                  {[
                    { cmd: "bold", label: "B", title: t("ui.bold"), cls: "font-bold" },
                    { cmd: "italic", label: "I", title: t("ui.italic"), cls: "italic" },
                    { cmd: "underline", label: "U", title: t("ui.underline"), cls: "underline" },
                  ].map((b) => (
                    <button
                      key={b.cmd}
                      type="button"
                      title={b.title}
                      aria-label={b.title}
                      onMouseDown={(e) => e.preventDefault()}
                      onClick={() => applySignatureCommand(b.cmd)}
                      className={`h-7 w-7 rounded-md text-[13px] text-slate-300 transition-colors hover:bg-white/[0.09] hover:text-slate-100 ${b.cls}`}
                    >
                      {b.label}
                    </button>
                  ))}
                  <span className="mx-1 h-4 w-px bg-white/10" />
                  <button
                    type="button"
                    title={t("ui.insertLink")}
                    aria-label={t("ui.insertLink")}
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => {
                      const url = window.prompt(t("ui.insertLink"), "https://");
                      if (url) applySignatureCommand("createLink", url);
                    }}
                    className="h-7 rounded-md px-2 text-[12px] text-slate-300 transition-colors hover:bg-white/[0.09] hover:text-slate-100"
                  >
                    {t("ui.insertLink")}
                  </button>
                  <button
                    type="button"
                    title={t("ui.insertImage")}
                    aria-label={t("ui.insertImage")}
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => {
                      const url = window.prompt(t("ui.insertImage"), "https://");
                      if (url) applySignatureCommand("insertImage", url);
                    }}
                    className="h-7 rounded-md px-2 text-[12px] text-slate-300 transition-colors hover:bg-white/[0.09] hover:text-slate-100"
                  >
                    {t("ui.insertImage")}
                  </button>
                </div>
                <div
                  id="signature-text"
                  ref={signatureRef}
                  contentEditable
                  suppressContentEditableWarning
                  role="textbox"
                  aria-multiline="true"
                  aria-label={t("settings.signatureTitle")}
                  onInput={(e) => setSignature((e.target as HTMLDivElement).innerHTML)}
                  onBlur={(e) => setSignature((e.target as HTMLDivElement).innerHTML)}
                  className="min-h-[170px] rounded-b-xl bg-dark-panel px-3.5 py-3 text-[13.5px] leading-relaxed text-slate-200 shadow-edge focus:outline-none"
                />
              </div>

              <Button onClick={saveSignature} variant="primary" size="md" className="self-start">
                {t("common.save")}
              </Button>
            </section>

            {/* What the signature will actually look like under a reply, so it
                is not written blind into a textarea. */}
            <section className={`${panel} min-w-[280px] flex-1`}>
              <div className="text-[11px] uppercase tracking-[0.08em] text-slate-400">
                {t("admin.csvPreview")}
              </div>
              <div className="rounded-xl bg-dark-card p-4 text-[13.5px] leading-relaxed text-slate-300">
                <div className="lm-rule my-2.5" />
                <pre className="whitespace-pre-wrap break-words font-sans">
                  {signature || t("settings.signaturePlaceholder")}
                </pre>
              </div>
            </section>
          </div>
        )}

        {activeTab === "language" && (
          <section className={panel}>
            <div>
              <h2 className={panelTitle}>{t("settings.language")}</h2>
              <p className={panelIntro}>{t("settings.subtitle")}</p>
            </div>
            <div className="max-w-[280px]">
              <LanguageSwitcher />
            </div>
          </section>
        )}
      </div>
    </div>
  );
}
