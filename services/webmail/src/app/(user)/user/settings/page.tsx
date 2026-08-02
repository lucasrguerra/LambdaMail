"use client";

import React, { useCallback, useEffect, useState } from "react";
import { useTranslations } from "../../../../i18n/provider";

export default function UserSettingsPage() {
  const t = useTranslations();
  const [mfaEnabled, setMfaEnabled] = useState(false);
  const [qrSecret, setQrSecret] = useState<string | null>(null);
  const [qrUri, setQrUri] = useState<string | null>(null);
  const [totpCode, setTotpCode] = useState("");
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);
  const [appPasswords, setAppPasswords] = useState<{ id: string; label: string; created_at?: string }[]>([]);
  const [newAppPassLabel, setNewAppPassLabel] = useState("");
  const [generatedAppPass, setGeneratedAppPass] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  const startTotpEnrollment = async () => {
    try {
      const res = await fetch("/api/v1/user/mfa/totp/enroll", { method: "POST" });
      const data = await res.json();
      setQrSecret(data.secret);
      setQrUri(data.uri);
    } catch {
      setMessage("Failed to initiate 2FA enrollment");
    }
  };

  const confirmTotpEnrollment = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const res = await fetch("/api/v1/user/mfa/totp/confirm", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ code: totpCode }),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.message || "TOTP verification failed");

      setMfaEnabled(true);
      setRecoveryCodes(data.recovery_codes);
      setQrSecret(null);
      setMessage("2FA successfully enabled! Save your recovery codes.");
    } catch (err: unknown) {
      setMessage(err instanceof Error ? err.message : "Error confirming 2FA");
    }
  };

  const loadAppPasswords = useCallback(async () => {
    try {
      const res = await fetch("/api/v1/user/app-passwords");
      if (res.ok) setAppPasswords(await res.json());
    } catch {
      // Leaving the list as it is beats replacing it with an empty one.
    }
  }, []);

  const loadProfile = useCallback(async () => {
    try {
      const res = await fetch("/api/v1/user/me");
      if (res.ok) {
        const data = await res.json();
        setMfaEnabled(Boolean(data.mfa_enrolled));
      }
    } catch {
      // Non-fatal: the enrollment panel simply starts collapsed.
    }
  }, []);

  useEffect(() => {
    void loadProfile();
    void loadAppPasswords();
  }, [loadProfile, loadAppPasswords]);

  const createNewAppPassword = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newAppPassLabel) return;
    try {
      const res = await fetch("/api/v1/user/app-passwords", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ label: newAppPassLabel }),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.message || "Could not create app password");
      // Shown once; the server keeps only an Argon2id hash of it.
      setGeneratedAppPass(data.password);
      setNewAppPassLabel("");
      await loadAppPasswords();
    } catch (err: unknown) {
      setMessage(err instanceof Error ? err.message : "Error creating app password");
    }
  };

  const revokeAppPassword = async (id: string) => {
    await fetch(`/api/v1/user/app-passwords/${id}`, { method: "DELETE" });
    await loadAppPasswords();
  };

  return (
    <div className="flex-1 p-8 bg-slate-950 overflow-y-auto">
      <div className="max-w-4xl mx-auto space-y-8">
        <div>
          <h1 className="text-2xl font-bold text-white mb-1">{t("settings.title")}</h1>
          <p className="text-xs text-slate-400">Manage 2FA TOTP (RFC 6238), App Passwords, and active webmail sessions.</p>
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
                Protect your webmail session with Google Authenticator, Aegis, Authy, or 1Password.
              </p>
            </div>
            <span className={`px-3 py-1 rounded-full text-xs font-bold ${mfaEnabled ? "badge-verified" : "badge-warning"}`}>
              {mfaEnabled ? "2FA ENABLED" : "2FA DISABLED"}
            </span>
          </div>

          {!mfaEnabled && !qrSecret && (
            <button
              onClick={startTotpEnrollment}
              className="py-2 px-4 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-medium text-xs transition-colors"
            >
              Start 2FA Setup &rarr;
            </button>
          )}

          {qrSecret && (
            <div className="p-4 rounded-xl bg-slate-900 border border-slate-800 space-y-4">
              <div className="text-xs text-slate-300">
                <strong>Step 1:</strong> Scan this URI in your authenticator app or copy the secret key.
              </div>
              <div className="p-3 bg-slate-950 rounded-lg text-xs font-mono text-indigo-400 break-all border border-slate-800">
                Secret: {qrSecret}
              </div>
              <div className="text-[11px] text-slate-500 font-mono">URI: {qrUri}</div>

              <form onSubmit={confirmTotpEnrollment} className="pt-2 flex items-center gap-3">
                <input
                  type="text"
                  value={totpCode}
                  onChange={(e) => setTotpCode(e.target.value)}
                  placeholder="Enter 6-digit code"
                  maxLength={6}
                  required
                  className="px-4 py-2 rounded-lg bg-slate-950 border border-slate-800 text-white text-xs font-mono text-center tracking-wider focus:outline-none focus:border-indigo-500"
                />
                <button
                  type="submit"
                  className="py-2 px-4 rounded-lg bg-emerald-600 hover:bg-emerald-500 text-white font-medium text-xs transition-colors"
                >
                  Confirm & Enable 2FA
                </button>
              </form>
            </div>
          )}

          {recoveryCodes && (
            <div className="mt-6 p-4 rounded-xl bg-slate-900 border border-emerald-500/30">
              <h3 className="text-sm font-bold text-white mb-2">10 Recovery Codes (Argon2id Hashed)</h3>
              <p className="text-xs text-slate-400 mb-3">
                Store these recovery codes in a safe password manager. Each code can be used once.
              </p>
              <div className="grid grid-cols-2 md:grid-cols-5 gap-2 text-center font-mono text-xs text-emerald-400 bg-slate-950 p-3 rounded-lg border border-slate-800">
                {recoveryCodes.map((code, idx) => (
                  <div key={idx} className="p-1 border border-slate-800 rounded">{code}</div>
                ))}
              </div>
            </div>
          )}
        </div>

        {/* APP PASSWORDS SECTION (ADR-010) */}
        <div className="glass-panel p-6 rounded-2xl border border-slate-800">
          <h2 className="text-lg font-bold text-white mb-1">{t("ui.appPasswordsSection")}</h2>
          <p className="text-xs text-slate-400 mb-4">
            Thunderbird, iOS Mail, and Android clients do not support 2FA. Generate dedicated high-entropy passwords for IMAP/SMTP.
          </p>

          <form onSubmit={createNewAppPassword} className="flex gap-3 mb-6">
            <input
              type="text"
              value={newAppPassLabel}
              onChange={(e) => setNewAppPassLabel(e.target.value)}
              placeholder="App Label (e.g. Thunderbird Laptop)"
              required
              className="flex-1 px-4 py-2 text-xs rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500"
            />
            <button
              type="submit"
              className="py-2 px-4 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white font-medium text-xs transition-colors"
            >
              Generate Password
            </button>
          </form>

          {generatedAppPass && (
            <div className="mb-6 p-4 rounded-xl bg-amber-500/10 border border-amber-500/30 text-amber-300 text-xs">
              <strong>{t("ui.appPasswordGenerated")}</strong>
              <div className="mt-1 font-mono text-sm font-bold text-amber-200 bg-slate-950 p-2 rounded border border-amber-500/20">
                {generatedAppPass}
              </div>
              <p className="mt-1 text-[11px] text-amber-400">{t("ui.copyPasswordNow")}</p>
            </div>
          )}

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
                  <button
                    type="button"
                    onClick={() => void revokeAppPassword(ap.id)}
                    className="text-red-400 hover:underline"
                  >
                    {t("common.delete")}
                  </button>
                </div>
              ))
            )}
          </div>
        </div>

        {/* ACTIVE SESSIONS SECTION */}
        <div className="glass-panel p-6 rounded-2xl border border-slate-800">
          <h2 className="text-lg font-bold text-white mb-1">{t("ui.activeSessions")}</h2>
          <p className="text-xs text-slate-400 mb-4">Individually revocable sessions with surface isolation.</p>
          <div className="p-3 rounded-lg bg-slate-900 border border-slate-800 flex items-center justify-between text-xs">
            <div>
              <div className="font-bold text-slate-200">{t("ui.currentSession")}</div>
              <div className="text-[10px] text-slate-400 font-mono">Cookie: Path=/user | Aud: lambdamail:user</div>
            </div>
            <span className="badge-verified px-2 py-0.5 rounded text-[10px]">{t("ui.active")}</span>
          </div>
        </div>
      </div>
    </div>
  );
}
