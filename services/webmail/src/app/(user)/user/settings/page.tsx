"use client";

import React, { useEffect, useState } from "react";
import { useTranslations } from "../../../../i18n/provider";

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

  // App passwords, sessions and password change. These have real endpoints
  // and were lost in a rewrite of this screen; the state below is server
  // state, not local bookkeeping.
  const [appPasswords, setAppPasswords] = useState<AppPassword[]>([]);
  const [newAppPassLabel, setNewAppPassLabel] = useState("");
  const [generatedAppPass, setGeneratedAppPass] = useState<string | null>(null);
  const [sessions, setSessions] = useState<WebSession[]>([]);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");

  // Vacation responder state
  const [vacationEnabled, setVacationEnabled] = useState(false);
  const [vacationSubject, setVacationSubject] = useState("Out of Office Auto-Reply");
  const [vacationBody, setVacationBody] = useState("Thank you for your message. I am currently out of office.");
  const [vacationSaved, setVacationSaved] = useState(false);

  // Signature state
  const [signature, setSignature] = useState("");
  const [sigSaved, setSigSaved] = useState(false);

  // Sieve rules state
  const [rules, setRules] = useState<SieveRule[]>([
    { id: "rule-1", field: "Subject", match: "contains", value: "[Newsletter]", action: "move", targetFolder: "Archive" },
  ]);
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
        if (data?.script) {
          // If script has vacation
          if (data.script.includes("vacation")) {
            setVacationEnabled(data.is_active);
          }
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
      setMessage("Failed to save signature");
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
      setMessage("Failed to save vacation responder");
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

    // Compile rules to Sieve script string & save to backend
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
      // Leaving the panels as they are beats blanking them on a hiccup.
    }
  };

  useEffect(() => {
    void loadAccount();
    // Loaded once on mount; every mutation below refreshes what it changed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
    // Shown once; the server keeps only an Argon2id hash of it.
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
    <div className="flex-1 p-8 bg-slate-950 overflow-y-auto">
      <div className="max-w-4xl mx-auto space-y-8">
        <div>
          <h1 className="text-2xl font-bold text-white mb-1">{t("settings.title")}</h1>
          <p className="text-xs text-slate-400">
            Manage 2FA TOTP (RFC 6238), Visual Sieve rules, Vacation auto-responder, and email signatures.
          </p>
        </div>

        {message && (
          <div className="p-4 rounded-xl bg-indigo-500/10 border border-indigo-500/30 text-indigo-300 text-sm">
            {message}
          </div>
        )}

        {/* 2FA TOTP SECTION */}
        <div className="glass-panel p-6 rounded-2xl border border-slate-800">
          <div className="flex items-center justify-between mb-4">
            <div>
              <h2 className="text-lg font-bold text-white">{t("ui.twoFactorSection")}</h2>
              <p className="text-xs text-slate-400">
                Protect your account with Google Authenticator, Aegis, Authy, or 1Password.
              </p>
            </div>
            <span className={`px-3 py-1 rounded-full text-xs font-bold ${mfaEnabled ? "badge-verified" : "badge-warning"}`}>
              {mfaEnabled ? t("settings.mfaEnabled") : t("settings.mfaDisabled")}
            </span>
          </div>

          {!mfaEnabled && !qrSecret && (
            <button
              onClick={async () => {
                const res = await fetch("/api/v1/user/mfa/totp/enroll", { method: "POST" });
                const data = await res.json();
                setQrSecret(data.secret);
                setQrUri(data.uri);
              }}
              className="py-2 px-4 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-medium text-xs transition-colors"
            >
              {t("settings.enableMfa")} -&gt;
            </button>
          )}

          {qrSecret && (
            <div className="p-4 rounded-xl bg-slate-900 border border-slate-800 space-y-4 text-xs">
              <div className="text-slate-300">
                Scan this URI in your authenticator app or copy secret key:
              </div>
              <div className="p-3 bg-slate-950 rounded-lg font-mono text-indigo-400 break-all border border-slate-800">
                Secret: {qrSecret}
              </div>
              <form
                onSubmit={async (e) => {
                  e.preventDefault();
                  const res = await fetch("/api/v1/user/mfa/totp/confirm", {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({ secret: qrSecret, code: totpCode }),
                  });
                  const data = await res.json();
                  if (res.ok) {
                    setMfaEnabled(true);
                    setRecoveryCodes(data.recovery_codes);
                    setQrSecret(null);
                  }
                }}
                className="pt-2 flex items-center gap-3"
              >
                <input
                  type="text"
                  value={totpCode}
                  onChange={(e) => setTotpCode(e.target.value)}
                  placeholder="6-digit code"
                  maxLength={6}
                  required
                  className="px-4 py-2 rounded-lg bg-slate-950 border border-slate-800 text-white text-xs font-mono text-center focus:outline-none focus:border-indigo-500"
                />
                <button
                  type="submit"
                  className="py-2 px-4 rounded-lg bg-emerald-600 hover:bg-emerald-500 text-white font-medium text-xs transition-colors"
                >
                  {t("common.confirm")}
                </button>
              </form>
            </div>
          )}

          {recoveryCodes && (
            <div className="mt-6 p-4 rounded-xl bg-slate-900 border border-emerald-500/30">
              <h3 className="text-sm font-bold text-white mb-2">{t("settings.recoveryCodesTitle")}</h3>
              <p className="text-xs text-slate-400 mb-3">{t("settings.saveRecoveryCodes")}</p>
              <div className="grid grid-cols-2 md:grid-cols-5 gap-2 text-center font-mono text-xs text-emerald-400 bg-slate-950 p-3 rounded-lg border border-slate-800">
                {recoveryCodes.map((code, idx) => (
                  <div key={idx} className="p-1 border border-slate-800 rounded">{code}</div>
                ))}
              </div>
            </div>
          )}
        </div>

        {/* VISUAL SIEVE RULES BUILDER (Section 10.3 / Section 14.2) */}
        <div className="glass-panel p-6 rounded-2xl border border-slate-800 space-y-4">
          <div>
            <h2 className="text-lg font-bold text-white">Visual Sieve Filters (RFC 5228)</h2>
            <p className="text-xs text-slate-400">Automated mail sorting and filtering rules.</p>
          </div>

          <form onSubmit={addSieveRule} className="grid grid-cols-1 md:grid-cols-5 gap-3 text-xs bg-slate-900/60 p-4 rounded-xl border border-slate-800">
            <select
              value={newField}
              onChange={(e) => setNewField(e.target.value as "From" | "Subject" | "Header")}
              className="px-3 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white focus:outline-none focus:border-indigo-500"
            >
              <option value="Subject">Subject</option>
              <option value="From">From</option>
              <option value="Header">Header</option>
            </select>

            <select
              value={newMatch}
              onChange={(e) => setNewMatch(e.target.value as "contains" | "equals" | "matches")}
              className="px-3 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white focus:outline-none focus:border-indigo-500"
            >
              <option value="contains">contains</option>
              <option value="equals">equals</option>
              <option value="matches">matches regex</option>
            </select>

            <input
              type="text"
              value={newValue}
              onChange={(e) => setNewValue(e.target.value)}
              placeholder="Value to match..."
              required
              className="px-3 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500"
            />

            <select
              value={newAction}
              onChange={(e) => setNewAction(e.target.value as "move" | "flag" | "discard" | "redirect")}
              className="px-3 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white focus:outline-none focus:border-indigo-500"
            >
              <option value="move">Move to Archive</option>
              <option value="flag">Flag Message</option>
              <option value="discard">Discard</option>
            </select>

            <button
              type="submit"
              className="py-2 px-4 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white font-medium transition-colors"
            >
              + Add Sieve Rule
            </button>
          </form>

          <div className="space-y-2 text-xs">
            {rules.map((r) => (
              <div key={r.id} className="flex items-center justify-between p-3 rounded-lg bg-slate-900 border border-slate-800">
                <span className="font-mono text-slate-200">
                  IF <strong>{r.field}</strong> {r.match} &quot;{r.value}&quot; -&gt; THEN {r.action.toUpperCase()}
                </span>
                <button
                  onClick={() => removeSieveRule(r.id)}
                  className="text-red-400 hover:underline font-medium"
                >
                  Delete
                </button>
              </div>
            ))}
          </div>
        </div>

        {/* VACATION AUTO-RESPONDER */}
        <div className="glass-panel p-6 rounded-2xl border border-slate-800 space-y-4">
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-lg font-bold text-white">Vacation Auto-Responder</h2>
              <p className="text-xs text-slate-400">Automatically reply to incoming messages when away.</p>
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
            <div className="p-3 rounded-lg bg-emerald-500/10 border border-emerald-500/30 text-emerald-300 text-xs">
              Vacation responder settings saved!
            </div>
          )}

          {vacationEnabled && (
            <form onSubmit={saveVacationResponder} className="space-y-3 text-xs">
              <div>
                <label className="font-medium text-slate-300 mb-1 block">Auto-Reply Subject</label>
                <input
                  type="text"
                  value={vacationSubject}
                  onChange={(e) => setVacationSubject(e.target.value)}
                  className="w-full px-3 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white focus:outline-none focus:border-indigo-500"
                />
              </div>

              <div>
                <label className="font-medium text-slate-300 mb-1 block">Auto-Reply Body Message</label>
                <textarea
                  rows={4}
                  value={vacationBody}
                  onChange={(e) => setVacationBody(e.target.value)}
                  className="w-full px-3 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white focus:outline-none focus:border-indigo-500"
                />
              </div>

              <button
                type="submit"
                className="py-2 px-4 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white font-medium transition-colors"
              >
                Save Vacation Responder
              </button>
            </form>
          )}
        </div>

        {/* EMAIL SIGNATURE MANAGER */}
        <div className="glass-panel p-6 rounded-2xl border border-slate-800 space-y-4 text-xs">
          <div>
            <h2 className="text-lg font-bold text-white mb-1">Email Signature</h2>
            <p className="text-slate-400">Signature automatically appended to composed messages.</p>
          </div>

          {sigSaved && (
            <div className="p-3 rounded-lg bg-emerald-500/10 border border-emerald-500/30 text-emerald-300">
              Signature saved successfully!
            </div>
          )}

          <textarea
            rows={4}
            value={signature}
            onChange={(e) => setSignature(e.target.value)}
            placeholder="Best regards,&#10;Your Name&#10;Your Title"
            className="w-full px-3 py-2 rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500"
          />

          <button
            onClick={saveSignature}
            className="py-2 px-4 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white font-medium transition-colors"
          >
            {t("common.save")}
          </button>
        </div>

        {/* APP PASSWORDS - real, issued and revoked through the API */}
        <div className="glass-panel p-6 rounded-2xl border border-slate-800">
          <h2 className="text-lg font-bold text-white mb-4">{t("ui.appPasswordsSection")}</h2>

          {generatedAppPass && (
            <div className="mb-4 p-3 rounded-lg bg-emerald-500/10 border border-emerald-500/30 text-emerald-300 text-xs">
              <div className="font-bold mb-1">{t("ui.appPasswordGenerated")}</div>
              <code className="block font-mono text-sm text-white break-all">{generatedAppPass}</code>
              <div className="mt-1">{t("ui.copyPasswordNow")}</div>
            </div>
          )}

          <form onSubmit={createAppPassword} className="flex gap-2 mb-4">
            <input
              type="text"
              value={newAppPassLabel}
              onChange={(e) => setNewAppPassLabel(e.target.value)}
              placeholder="Thunderbird"
              className="flex-1 px-3 py-2 text-xs rounded-lg bg-slate-900 border border-slate-800 text-white"
            />
            <button
              type="submit"
              className="px-4 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white text-xs font-medium"
            >
              {t("settings.generateAppPassword")}
            </button>
          </form>

          <div className="space-y-2">
            {appPasswords.length === 0 ? (
              <div className="text-xs text-slate-500 italic">{t("ui.noAppPasswords")}</div>
            ) : (
              appPasswords.map((ap) => (
                <div key={ap.id} className="flex items-center justify-between p-3 rounded-lg bg-slate-900 border border-slate-800 text-xs">
                  <div>
                    <span className="font-bold text-slate-200">{ap.label}</span>
                    <span className="text-[10px] text-slate-500 ml-2">
                      {ap.created_at ? new Date(ap.created_at).toLocaleDateString() : ""}
                    </span>
                  </div>
                  <button type="button" onClick={() => void revokeAppPassword(ap.id)} className="text-red-400 hover:underline">
                    {t("common.delete")}
                  </button>
                </div>
              ))
            )}
          </div>
        </div>

        {/* ACTIVE SESSIONS - revoking one takes effect on the next request */}
        <div className="glass-panel p-6 rounded-2xl border border-slate-800">
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-lg font-bold text-white">{t("ui.activeSessions")}</h2>
            <button
              type="button"
              onClick={() => void revokeOtherSessions()}
              className="text-xs px-3 py-1.5 rounded-lg border border-slate-700 text-slate-300 hover:text-white"
            >
              {t("ui.signOutOthers")}
            </button>
          </div>

          <div className="space-y-2">
            {sessions.map((session) => (
              <div key={session.id} className="flex items-center justify-between p-3 rounded-lg bg-slate-900 border border-slate-800 text-xs">
                <div className="min-w-0">
                  <div className="text-slate-200 truncate">
                    {session.ip_address ?? "-"}
                    <span className="text-[10px] text-slate-500 ml-2 uppercase">{session.surface}</span>
                  </div>
                  <div className="text-[10px] text-slate-500 truncate">{session.user_agent ?? ""}</div>
                </div>
                {session.current ? (
                  <span className="text-[10px] text-emerald-400">{t("ui.currentSession")}</span>
                ) : (
                  <button type="button" onClick={() => void revokeSession(session.id)} className="text-red-400 hover:underline">
                    {t("common.delete")}
                  </button>
                )}
              </div>
            ))}
          </div>
        </div>

        {/* PASSWORD CHANGE - takes every other session down with it */}
        <div className="glass-panel p-6 rounded-2xl border border-slate-800">
          <h2 className="text-lg font-bold text-white mb-4">{t("ui.changePassword")}</h2>
          <form onSubmit={changePassword} className="space-y-3">
            <input
              type="password"
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              placeholder={t("ui.currentPassword")}
              required
              className="w-full px-3 py-2 text-xs rounded-lg bg-slate-900 border border-slate-800 text-white"
            />
            <input
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              placeholder={t("ui.newPassword")}
              minLength={12}
              required
              className="w-full px-3 py-2 text-xs rounded-lg bg-slate-900 border border-slate-800 text-white"
            />
            <button type="submit" className="py-2 px-4 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white text-xs font-medium">
              {t("common.save")}
            </button>
          </form>
        </div>
      </div>
    </div>
  );
}
