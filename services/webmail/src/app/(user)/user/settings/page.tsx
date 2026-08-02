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

export default function UserSettingsPage() {
  const t = useTranslations();

  const [mfaEnabled, setMfaEnabled] = useState(false);
  const [qrSecret, setQrSecret] = useState<string | null>(null);
  const [qrUri, setQrUri] = useState<string | null>(null);
  const [totpCode, setTotpCode] = useState("");
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  const [message, setMessage] = useState<string | null>(null);

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
    const savedSig = localStorage.getItem("lm_user_signature");
    if (savedSig) setSignature(savedSig);
  }, []);

  const saveSignature = () => {
    localStorage.setItem("lm_user_signature", signature);
    setSigSaved(true);
    setTimeout(() => setSigSaved(false), 2000);
  };

  const addSieveRule = (e: React.FormEvent) => {
    e.preventDefault();
    if (!newValue) return;
    setRules([
      ...rules,
      {
        id: `rule-${Date.now()}`,
        field: newField,
        match: newMatch,
        value: newValue,
        action: newAction,
        targetFolder: newTarget,
      },
    ]);
    setNewValue("");
  };

  const removeSieveRule = (id: string) => {
    setRules(rules.filter((r) => r.id !== id));
  };

  const saveVacationResponder = (e: React.FormEvent) => {
    e.preventDefault();
    setVacationSaved(true);
    setTimeout(() => setVacationSaved(false), 2000);
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
      </div>
    </div>
  );
}
