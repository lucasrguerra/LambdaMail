"use client";

import React, { useEffect, useState } from "react";
import {
  Shield,
  Filter,
  Palmtree,
  FileText,
  KeyRound,
  Monitor,
  CheckCircle2,
  Trash2,
  Plus,
  Lock,
} from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { Card } from "../../../../components/ui/Card";
import { Badge } from "../../../../components/ui/Badge";
import { Button } from "../../../../components/ui/Button";
import { TotpEnrolment, RecoveryCodes } from "../../../../components/TotpEnrolment";

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

export default function UserSettingsPage() {
  const t = useTranslations();

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
  const [sigSaved, setSigSaved] = useState(false);

  // Starts empty rather than with a sample rule. A seeded "[Boletim]" filter
  // used to sit here, and because saving posts the whole list, the first time
  // anyone added or removed a rule that invented filter was written into their
  // real Sieve script and began moving their mail.
  const [rules, setRules] = useState<SieveRule[]>([]);
  const [newField, setNewField] = useState<"From" | "Subject" | "Header">("Subject");
  const [newMatch, setNewMatch] = useState<"contains" | "equals" | "matches">("contains");
  const [newValue, setNewValue] = useState("");
  const [newAction, setNewAction] = useState<"move" | "flag" | "discard" | "redirect">("move");
  const [newTarget, setNewTarget] = useState("Archive");

  useEffect(() => {
    fetch("/api/v1/user/preferences")
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => {
        if (data?.signature) setSignature(data.signature);
      })
      .catch(() => undefined);

    fetch("/api/v1/user/sieve")
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => {
        if (data?.script && data.script.includes("vacation")) {
          setVacationEnabled(data.is_active);
        }
      })
      .catch(() => undefined);
  }, []);

  const saveSignature = async () => {
    try {
      await fetch("/api/v1/user/preferences", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ signature, auto_save_drafts: true }),
      });
      localStorage.setItem("lm_user_signature", signature);
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

  const addSieveRule = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newValue) return;
    const updated = [
      ...rules,
      {
        id: `rule-${Date.now()}`,
        field: newField,
        match: newMatch,
        value: newValue,
        action: newAction,
        targetFolder: newTarget,
      },
    ];
    setRules(updated);
    setNewValue("");

    const scriptLines = updated.map((r) =>
      `if header :${r.match} "${r.field}" "${r.value}" { fileinto "${r.targetFolder || "INBOX"}"; }`
    );
    await fetch("/api/v1/user/sieve", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "user-rules", script: scriptLines.join("\n"), is_active: true }),
    }).catch(() => undefined);
  };

  const removeSieveRule = async (id: string) => {
    const updated = rules.filter((r) => r.id !== id);
    setRules(updated);
    const scriptLines = updated.map((r) =>
      `if header :${r.match} "${r.field}" "${r.value}" { fileinto "${r.targetFolder || "INBOX"}"; }`
    );
    await fetch("/api/v1/user/sieve", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "user-rules", script: scriptLines.join("\n"), is_active: true }),
    }).catch(() => undefined);
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

  return (
    <div className="flex-1 p-6 md:p-8 bg-dark-bg overflow-y-auto">
      <div className="max-w-4xl mx-auto space-y-8">
        <div>
          <h1 className="text-2xl md:text-3xl font-extrabold text-white tracking-tight">
            {t("settings.title")}
          </h1>
          <p className="text-sm text-slate-400 mt-1">{t("settings.subtitle")}</p>
        </div>

        {message && (
          <div className="p-4 rounded-xl bg-indigo-500/15 border border-indigo-500/30 text-indigo-300 text-xs flex items-center gap-2">
            <CheckCircle2 className="w-4 h-4 text-indigo-400" />
            <span>{message}</span>
          </div>
        )}

        {/* 2FA TOTP SECTION */}
        <Card className="space-y-4">
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-base font-bold text-white flex items-center gap-2">
                <Shield className="w-5 h-5 text-indigo-400" />
                {t("ui.twoFactorSection")}
              </h2>
              <p className="text-xs text-slate-400 mt-0.5">{t("settings.twoFactorIntro")}</p>
            </div>
            <Badge variant={mfaEnabled ? "success" : "warning"}>
              {mfaEnabled ? t("settings.mfaEnabled") : t("settings.mfaDisabled")}
            </Badge>
          </div>

          {!mfaEnabled && !qrSecret && (
            <Button
              variant="primary"
              size="sm"
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
            <div className="p-4 rounded-xl bg-slate-900/90 border border-slate-800 space-y-4 text-xs">
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
                className="text-[11px] text-slate-400 underline hover:text-slate-200"
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
                className="pt-2 flex items-center gap-3"
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
                  className="px-4 py-2 rounded-xl bg-slate-950 border border-slate-800 text-white text-xs font-mono text-center focus:outline-none focus:border-indigo-500"
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
        </Card>

        {/* VISUAL SIEVE RULES BUILDER */}
        <Card className="space-y-4">
          <div>
            <h2 className="text-base font-bold text-white flex items-center gap-2">
              <Filter className="w-5 h-5 text-indigo-400" />
              {t("settings.sieveTitle")}
            </h2>
            <p className="text-xs text-slate-400 mt-0.5">{t("settings.sieveIntro")}</p>
          </div>

          <form onSubmit={addSieveRule} className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-5 gap-3 text-xs bg-slate-900/60 p-4 rounded-xl border border-slate-800">
            <select
              value={newField}
              onChange={(e) => setNewField(e.target.value as "From" | "Subject" | "Header")}
              className="px-3 py-2 rounded-xl bg-slate-900 border border-slate-800 text-white focus:outline-none focus:border-indigo-500"
            >
              <option value="Subject">{t("settings.fieldSubject")}</option>
              <option value="From">{t("settings.fieldFrom")}</option>
              <option value="Header">{t("settings.fieldHeader")}</option>
            </select>

            <select
              value={newMatch}
              onChange={(e) => setNewMatch(e.target.value as "contains" | "equals" | "matches")}
              className="px-3 py-2 rounded-xl bg-slate-900 border border-slate-800 text-white focus:outline-none focus:border-indigo-500"
            >
              <option value="contains">{t("settings.matchContains")}</option>
              <option value="equals">{t("settings.matchEquals")}</option>
              <option value="matches">{t("settings.matchRegex")}</option>
            </select>

            <input
              type="text"
              value={newValue}
              onChange={(e) => setNewValue(e.target.value)}
              placeholder={t("settings.valuePlaceholder")}
              required
              className="px-3 py-2 rounded-xl bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500"
            />

            <select
              value={newAction}
              onChange={(e) => setNewAction(e.target.value as "move" | "flag" | "discard" | "redirect")}
              className="px-3 py-2 rounded-xl bg-slate-900 border border-slate-800 text-white focus:outline-none focus:border-indigo-500"
            >
              <option value="move">{t("settings.actionMove")}</option>
              <option value="flag">{t("settings.actionFlag")}</option>
              <option value="discard">{t("settings.actionDiscard")}</option>
            </select>

            <Button type="submit" variant="primary" size="sm" className="w-full">
              <Plus className="w-4 h-4" />
              <span>{t("settings.addRule")}</span>
            </Button>
          </form>

          <div className="space-y-2 text-xs">
            {rules.map((r) => (
              <div key={r.id} className="flex items-center justify-between p-3 rounded-xl bg-slate-900/80 border border-slate-800">
                <span className="font-mono text-slate-200">
                  {t("settings.ruleIf")} <strong>{r.field}</strong> {r.match} &quot;{r.value}&quot; &rarr;{" "}
                  {t("settings.ruleThen")} {r.action.toUpperCase()}
                </span>
                <button
                  onClick={() => removeSieveRule(r.id)}
                  className="text-rose-400 hover:text-rose-300 transition-colors p-1"
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            ))}
          </div>
        </Card>

        {/* VACATION AUTO-RESPONDER */}
        <Card className="space-y-4">
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-base font-bold text-white flex items-center gap-2">
                <Palmtree className="w-5 h-5 text-indigo-400" />
                {t("settings.vacationTitle")}
              </h2>
              <p className="text-xs text-slate-400 mt-0.5">{t("settings.vacationIntro")}</p>
            </div>
            <label className="relative inline-flex items-center cursor-pointer">
              <input
                type="checkbox"
                checked={vacationEnabled}
                onChange={(e) => setVacationEnabled(e.target.checked)}
                className="sr-only peer"
              />
              <div className="w-11 h-6 bg-slate-800 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all peer-checked:bg-indigo-600" />
            </label>
          </div>

          {vacationSaved && (
            <div className="p-3.5 rounded-xl bg-emerald-500/15 border border-emerald-500/30 text-emerald-300 text-xs flex items-center gap-2">
              <CheckCircle2 className="w-4 h-4 text-emerald-400" />
              <span>{t("settings.settingsSaved")}</span>
            </div>
          )}

          {vacationEnabled && (
            <form onSubmit={saveVacationResponder} className="space-y-3 text-xs">
              <div>
                <label className="font-semibold text-slate-300 mb-1.5 block">{t("settings.vacationSubjectLabel")}</label>
                <input
                  type="text"
                  value={vacationSubject}
                  onChange={(e) => setVacationSubject(e.target.value)}
                  className="w-full px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white focus:outline-none focus:border-indigo-500"
                />
              </div>

              <div>
                <label className="font-semibold text-slate-300 mb-1.5 block">{t("settings.vacationBodyLabel")}</label>
                <textarea
                  rows={4}
                  value={vacationBody}
                  onChange={(e) => setVacationBody(e.target.value)}
                  className="w-full px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white focus:outline-none focus:border-indigo-500"
                />
              </div>

              <Button type="submit" variant="primary" size="sm">
                {t("settings.saveAutoReply")}
              </Button>
            </form>
          )}
        </Card>

        {/* EMAIL SIGNATURE MANAGER */}
        <Card className="space-y-4 text-xs">
          <div>
            <h2 className="text-base font-bold text-white flex items-center gap-2">
              <FileText className="w-5 h-5 text-indigo-400" />
              {t("settings.signatureTitle")}
            </h2>
            <p className="text-slate-400 mt-0.5">{t("settings.signatureIntro")}</p>
          </div>

          {sigSaved && (
            <div className="p-3.5 rounded-xl bg-emerald-500/15 border border-emerald-500/30 text-emerald-300 flex items-center gap-2">
              <CheckCircle2 className="w-4 h-4 text-emerald-400" />
              <span>{t("settings.signatureSaved")}</span>
            </div>
          )}

          <textarea
            rows={4}
            value={signature}
            onChange={(e) => setSignature(e.target.value)}
            placeholder={t("settings.signaturePlaceholder")}
            className="w-full px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500"
          />

          <Button onClick={saveSignature} variant="primary" size="sm">
            {t("common.save")}
          </Button>
        </Card>

        {/* APP PASSWORDS */}
        <Card className="space-y-4">
          <h2 className="text-base font-bold text-white flex items-center gap-2">
            <KeyRound className="w-5 h-5 text-indigo-400" />
            {t("ui.appPasswordsSection")}
          </h2>

          {generatedAppPass && (
            <div className="p-4 rounded-xl bg-emerald-500/15 border border-emerald-500/30 text-emerald-300 text-xs space-y-1">
              <div className="font-bold">{t("ui.appPasswordGenerated")}</div>
              <code className="block font-mono text-sm text-white break-all bg-slate-950 p-2 rounded-lg border border-slate-800">
                {generatedAppPass}
              </code>
              <div className="text-[10px] text-emerald-400">{t("ui.copyPasswordNow")}</div>
            </div>
          )}

          <form onSubmit={createAppPassword} className="flex gap-2">
            <input
              type="text"
              value={newAppPassLabel}
              onChange={(e) => setNewAppPassLabel(e.target.value)}
              placeholder={t("settings.appPasswordPlaceholder")}
              className="flex-1 px-3.5 py-2 text-xs rounded-xl bg-slate-900/90 border border-slate-800 text-white focus:outline-none focus:border-indigo-500"
            />
            <Button type="submit" variant="primary" size="sm">
              {t("settings.generateAppPassword")}
            </Button>
          </form>

          <div className="space-y-2">
            {appPasswords.length === 0 ? (
              <div className="text-xs text-slate-500 italic">{t("ui.noAppPasswords")}</div>
            ) : (
              appPasswords.map((ap) => (
                <div key={ap.id} className="flex items-center justify-between p-3 rounded-xl bg-slate-900/80 border border-slate-800 text-xs">
                  <div>
                    <span className="font-bold text-slate-200">{ap.label}</span>
                    <span className="text-[10px] text-slate-500 ml-2">
                      {ap.created_at ? new Date(ap.created_at).toLocaleDateString() : ""}
                    </span>
                  </div>
                  <button type="button" onClick={() => void revokeAppPassword(ap.id)} className="text-rose-400 hover:text-rose-300 p-1">
                    <Trash2 className="w-4 h-4" />
                  </button>
                </div>
              ))
            )}
          </div>
        </Card>

        {/* ACTIVE SESSIONS */}
        <Card className="space-y-4">
          <div className="flex items-center justify-between">
            <h2 className="text-base font-bold text-white flex items-center gap-2">
              <Monitor className="w-5 h-5 text-indigo-400" />
              {t("ui.activeSessions")}
            </h2>
            <Button variant="ghost" size="sm" onClick={() => void revokeOtherSessions()}>
              {t("ui.signOutOthers")}
            </Button>
          </div>

          <div className="space-y-2">
            {sessions.map((session) => (
              <div key={session.id} className="flex items-center justify-between p-3 rounded-xl bg-slate-900/80 border border-slate-800 text-xs">
                <div className="min-w-0">
                  <div className="text-slate-200 truncate font-mono">
                    {session.ip_address ?? "-"}
                    <span className="text-[10px] text-slate-500 ml-2 uppercase font-sans font-bold">{session.surface}</span>
                  </div>
                  <div className="text-[10px] text-slate-500 truncate">{session.user_agent ?? ""}</div>
                </div>
                {session.current ? (
                  <Badge variant="success">{t("ui.currentSession")}</Badge>
                ) : (
                  <button type="button" onClick={() => void revokeSession(session.id)} className="text-rose-400 hover:text-rose-300 p-1">
                    <Trash2 className="w-4 h-4" />
                  </button>
                )}
              </div>
            ))}
          </div>
        </Card>

        {/* PASSWORD CHANGE */}
        <Card className="space-y-4">
          <h2 className="text-base font-bold text-white flex items-center gap-2">
            <Lock className="w-5 h-5 text-indigo-400" />
            {t("ui.changePassword")}
          </h2>
          <form onSubmit={changePassword} className="space-y-3">
            <input
              type="password"
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              placeholder={t("ui.currentPassword")}
              required
              className="w-full px-3.5 py-2.5 text-xs rounded-xl bg-slate-900/90 border border-slate-800 text-white focus:outline-none focus:border-indigo-500"
            />
            <input
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              placeholder={t("ui.newPassword")}
              minLength={12}
              required
              className="w-full px-3.5 py-2.5 text-xs rounded-xl bg-slate-900/90 border border-slate-800 text-white focus:outline-none focus:border-indigo-500"
            />
            <Button type="submit" variant="primary" size="sm">
              {t("common.save")}
            </Button>
          </form>
        </Card>
      </div>
    </div>
  );
}
