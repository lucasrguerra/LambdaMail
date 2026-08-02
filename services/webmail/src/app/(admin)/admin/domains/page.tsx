"use client";

import React, { useCallback, useEffect, useState } from "react";
import { Globe, RefreshCw, Plus, KeyRound, CheckCircle2, AlertTriangle } from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { Card } from "../../../../components/ui/Card";
import { Badge } from "../../../../components/ui/Badge";
import { Button } from "../../../../components/ui/Button";

/**
 * Domains, read from the database.
 *
 * The previous version of this screen was a mockup end to end. It rendered a
 * fixed table of thirteen DNS records for "example.com", every one of them
 * marked VERIFIED; its onboarding wizard had four steps of which only the first
 * called anything, step three printing "Cloudflare connection validated, 13
 * records created and propagated" without a request behind it; and its DKIM
 * rotation posted {domain_id: "default-domain"} to an endpoint whose field is
 * "domain", swallowed the resulting 400, and displayed a made-up TXT record as
 * though a key had been issued. Rotation therefore never once worked from here.
 *
 * What is shown now is what the API returns, and the one thing the backend
 * cannot yet do - verify individual DNS records against a resolver - says so
 * instead of drawing a table of green ticks.
 */

interface Domain {
  id: string;
  name: string;
  dns_status: string;
  dmarc_policy: string | null;
  mta_sts_mode: string | null;
  dane_enabled: boolean;
  is_active: boolean;
  dns_last_checked_at: string | null;
  mailbox_count: number;
}

function statusVariant(status: string): "success" | "warning" | "danger" {
  if (status === "VERIFIED") return "success";
  if (status === "PENDING" || status === "PARTIAL") return "warning";
  return "danger";
}

export default function AdminDomainsPage() {
  const t = useTranslations();

  const [activeTab, setActiveTab] = useState<"domains" | "onboarding" | "dkim">("domains");
  const [domains, setDomains] = useState<Domain[]>([]);
  const [selectedId, setSelectedId] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const [reconciling, setReconciling] = useState(false);
  const [onboardDomain, setOnboardDomain] = useState("");
  const [onboarding, setOnboarding] = useState(false);

  const [dkimAlgorithm, setDkimAlgorithm] = useState<"ed25519" | "rsa2048">("ed25519");
  const [dkimSelector, setDkimSelector] = useState("s1");
  const [dkimRecord, setDkimRecord] = useState<{ name: string; value: string; overlapDays: number } | null>(null);
  const [rotating, setRotating] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await fetch("/api/v1/admin/domains");
      if (!res.ok) throw new Error();
      const data: Domain[] = await res.json();
      setDomains(data);
      // Keeps the current pick if it still exists, so a refresh after an action
      // does not silently retarget the next operation at another domain.
      setSelectedId((prev) => (data.some((d) => d.id === prev) ? prev : (data[0]?.id ?? "")));
      setError(null);
    } catch {
      setDomains([]);
      setError(t("errors.loadFailed"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  const selected = domains.find((d) => d.id === selectedId) ?? null;

  const handleReconcile = async () => {
    if (!selected) return;
    setReconciling(true);
    setNotice(null);
    setError(null);
    try {
      // The real domain id, not the literal string "default-domain" that used
      // to be sent here for every domain on the server.
      const res = await fetch("/api/v1/admin/domains/reconcile", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ domain_id: selected.id }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.message);
      setNotice(data.message ?? t("settings.settingsSaved"));
      await load();
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : t("errors.serverError"));
    } finally {
      setReconciling(false);
    }
  };

  const handleOnboard = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!onboardDomain.trim()) return;
    setOnboarding(true);
    setNotice(null);
    setError(null);
    try {
      const res = await fetch("/api/v1/admin/domains/onboard", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ domain: onboardDomain.trim() }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.message);
      setOnboardDomain("");
      setNotice(`${data.name}`);
      await load();
      setActiveTab("domains");
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : t("errors.serverError"));
    } finally {
      setOnboarding(false);
    }
  };

  const handleRotateDkim = async () => {
    if (!selected) return;
    setRotating(true);
    setNotice(null);
    setError(null);
    setDkimRecord(null);
    try {
      // The endpoint's field is "domain" and it wants the name. Sending the
      // wrong field is what made every previous rotation fail unnoticed.
      const res = await fetch("/api/v1/admin/dkim/rotate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          domain: selected.name,
          selector: dkimSelector,
          algorithm: dkimAlgorithm,
        }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.message);
      // The key that was actually generated and sealed, not a sample string.
      setDkimRecord({
        name: `${data.selector}._domainkey.${data.domain}`,
        value: data.public_key_record,
        overlapDays: data.overlap_days,
      });
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : t("errors.serverError"));
    } finally {
      setRotating(false);
    }
  };

  const tabs = [
    { id: "domains" as const, label: t("admin.domainsTitle"), icon: Globe },
    { id: "onboarding" as const, label: t("common.add"), icon: Plus },
    { id: "dkim" as const, label: "DKIM", icon: KeyRound },
  ];

  return (
    <div className="p-6 md:p-8 space-y-6 max-w-7xl mx-auto">
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl md:text-3xl font-extrabold text-white tracking-tight">
            {t("admin.domainsTitle")}
          </h1>
        </div>

        <div className="flex bg-slate-900/90 p-1.5 rounded-xl border border-slate-800 text-xs font-medium self-start md:self-auto">
          {tabs.map((tab) => {
            const Icon = tab.icon;
            return (
              <button
                key={tab.id}
                type="button"
                onClick={() => setActiveTab(tab.id)}
                className={`flex items-center gap-2 px-3.5 py-2 rounded-lg transition-all ${
                  activeTab === tab.id
                    ? "bg-emerald-600 text-white shadow-sm"
                    : "text-slate-400 hover:text-slate-200"
                }`}
              >
                <Icon className="w-3.5 h-3.5" />
                <span>{tab.label}</span>
              </button>
            );
          })}
        </div>
      </div>

      {notice && (
        <div className="p-4 rounded-xl bg-emerald-500/15 border border-emerald-500/30 text-emerald-300 text-xs flex items-center gap-2">
          <CheckCircle2 className="w-4 h-4 flex-shrink-0" />
          <span>{notice}</span>
        </div>
      )}
      {error && (
        <div className="p-4 rounded-xl bg-rose-500/15 border border-rose-500/30 text-rose-300 text-xs">
          {error}
        </div>
      )}

      {activeTab === "domains" && (
        <div className="space-y-4">
          {domains.length === 0 && !loading ? (
            <Card className="p-8 text-center text-xs text-slate-500">{t("admin.noDomains")}</Card>
          ) : (
            domains.map((d) => (
              <Card key={d.id} className="p-4">
                <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
                  <div className="flex items-center gap-3 min-w-0">
                    <div className="w-10 h-10 rounded-xl bg-indigo-500/15 border border-indigo-500/30 flex items-center justify-center text-indigo-400 flex-shrink-0">
                      <Globe className="w-5 h-5" />
                    </div>
                    <div className="min-w-0">
                      <div className="text-base font-bold text-white truncate">{d.name}</div>
                      <div className="text-xs text-slate-400">
                        {t("admin.mailboxCount", { count: d.mailbox_count })}
                        {d.dmarc_policy ? ` | DMARC: ${d.dmarc_policy}` : ""}
                        {d.mta_sts_mode ? ` | MTA-STS: ${d.mta_sts_mode}` : ""}
                        {d.dane_enabled ? " | DANE" : ""}
                      </div>
                    </div>
                  </div>
                  <div className="flex items-center gap-2 flex-shrink-0">
                    <Badge variant={statusVariant(d.dns_status)}>{d.dns_status}</Badge>
                    <Button
                      variant={selectedId === d.id ? "primary" : "outline"}
                      size="sm"
                      onClick={() => setSelectedId(d.id)}
                    >
                      {selectedId === d.id ? <CheckCircle2 className="w-3.5 h-3.5" /> : null}
                      <span>{selectedId === d.id ? t("common.status") : t("common.add")}</span>
                    </Button>
                  </div>
                </div>
                <div className="mt-3 pt-3 border-t border-slate-800 text-[11px] text-slate-500 font-mono">
                  {t("admin.lastChecked", {
                    when: d.dns_last_checked_at
                      ? new Date(d.dns_last_checked_at).toLocaleString()
                      : t("common.never"),
                  })}
                </div>
              </Card>
            ))
          )}

          {/* Says what reconciliation currently does. The old copy announced
              "Cloudflare sync finished: all 13 DNS records verified" on every
              press - including when the request failed - while the endpoint
              behind it only writes an audit entry. */}
          <Card className="space-y-3">
            <div className="flex items-start gap-2 text-xs text-amber-300">
              <AlertTriangle className="w-4 h-4 flex-shrink-0 mt-0.5" />
              <p>{t("admin.reconcileNotImplemented")}</p>
            </div>
            <Button variant="outline" size="sm" onClick={handleReconcile} disabled={reconciling || !selected}>
              <RefreshCw className={`w-3.5 h-3.5 ${reconciling ? "animate-spin" : ""}`} />
              <span>{t("admin.reconcileDns")}</span>
            </Button>
          </Card>
        </div>
      )}

      {activeTab === "onboarding" && (
        <Card className="max-w-2xl space-y-4">
          <div>
            <h2 className="text-lg font-bold text-white mb-1">{t("admin.onboardTitle")}</h2>
            <p className="text-xs text-slate-400">{t("admin.onboardIntro")}</p>
          </div>
          <form onSubmit={handleOnboard} className="space-y-3 text-xs">
            <div>
              <label className="font-semibold text-slate-300 block mb-1">{t("admin.domainName")}</label>
              <input
                type="text"
                value={onboardDomain}
                onChange={(e) => setOnboardDomain(e.target.value)}
                placeholder="example.org"
                required
                className="w-full px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-emerald-500"
              />
            </div>
            <Button type="submit" variant="primary" size="md" disabled={onboarding}>
              <Plus className="w-4 h-4" />
              <span>{onboarding ? t("common.loading") : t("common.add")}</span>
            </Button>
          </form>
        </Card>
      )}

      {activeTab === "dkim" && (
        <Card className="max-w-3xl space-y-5">
          <div>
            <h2 className="text-lg font-bold text-white mb-1 flex items-center gap-2">
              <KeyRound className="w-5 h-5 text-indigo-400" />
              {t("admin.dkimTitle")}
            </h2>
            <p className="text-xs text-slate-400">
              {selected ? selected.name : t("admin.noDomains")}
            </p>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 text-xs">
            <div>
              <label className="font-semibold text-slate-300 mb-1.5 block">{t("admin.dkimAlgorithm")}</label>
              <select
                value={dkimAlgorithm}
                onChange={(e) => setDkimAlgorithm(e.target.value as "ed25519" | "rsa2048")}
                className="w-full px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white focus:outline-none focus:border-emerald-500"
              >
                <option value="ed25519">Ed25519</option>
                <option value="rsa2048">RSA-2048</option>
              </select>
            </div>
            <div>
              <label className="font-semibold text-slate-300 mb-1.5 block">{t("admin.dkimSelector")}</label>
              <input
                type="text"
                value={dkimSelector}
                onChange={(e) => setDkimSelector(e.target.value)}
                pattern="[A-Za-z0-9-]{1,63}"
                className="w-full px-3.5 py-2.5 rounded-xl bg-slate-900/90 border border-slate-800 text-white font-mono focus:outline-none focus:border-emerald-500"
              />
            </div>
          </div>

          <Button variant="primary" size="md" onClick={handleRotateDkim} disabled={rotating || !selected}>
            <KeyRound className="w-4 h-4" />
            <span>{rotating ? t("common.loading") : t("admin.dkimRotate")}</span>
          </Button>

          {dkimRecord && (
            <div className="p-4 bg-slate-900/90 rounded-xl border border-slate-800 space-y-3 text-xs">
              <div className="font-bold text-white">{t("admin.dkimPublishRecord")}</div>
              <div className="p-3 bg-slate-950 rounded-lg border border-slate-800 font-mono text-[11px] text-emerald-400 break-all">
                {dkimRecord.name} IN TXT &quot;{dkimRecord.value}&quot;
              </div>
              <div className="p-3.5 rounded-xl bg-amber-500/15 border border-amber-500/25 text-amber-300">
                {t("admin.dkimOverlap", { days: dkimRecord.overlapDays })}
              </div>
            </div>
          )}
        </Card>
      )}
    </div>
  );
}
