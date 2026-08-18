"use client";

import React, { useEffect, useState } from "react";
import { motion } from "framer-motion";
import {
  Inbox,
  Send,
  Layers,
  HardDrive,
  Server,
  ShieldCheck,
  Globe,
  RefreshCw,
  Zap,
} from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { Badge } from "../../../../components/ui/Badge";
import { Button } from "../../../../components/ui/Button";

/** Exactly the fields /api/v1/admin/dashboard returns - see dashboardStats(). */
interface DashboardMetrics {
  inbound_24h: number;
  outbound_24h: number;
  queue_depth: number;
  bounce_rate: number;
  domains_verified: number;
  domains_total: number;
  mailboxes_active: number;
  storage_used_bytes: number;
  storage_quota_bytes: number;
}

function formatBytes(bytes: number): string {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return `${(bytes / 1024 ** i).toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

export default function AdminDashboardPage() {
  const t = useTranslations();
  const [metrics, setMetrics] = useState<DashboardMetrics | null>(null);
  const [loading, setLoading] = useState(false);

  const fetchMetrics = () => {
    setLoading(true);
    fetch("/api/v1/admin/dashboard")
      .then((res) => (res.ok ? res.json() : null))
      .then((data) => setMetrics(data))
      .catch(() => setMetrics(null))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    fetchMetrics();
  }, []);

  /**
   * A missing figure reads as "no data", never as a number.
   *
   * Each of these used to fall back to an invented constant - 1420 received,
   * 890 sent, 3 queued, 18.4% disk - so an unreachable API produced a dashboard
   * of plausible traffic for a server that might not have delivered a single
   * message. Two of those fields (disk_used_percent, spam_score_avg) were never
   * returned by the endpoint at all, so the fake disk figure was what everyone
   * always saw.
   */
  const shown = (value: number | undefined, format?: (n: number) => string) =>
    value === undefined || value === null ? t("ui.noData") : format ? format(value) : String(value);

  /* The KPI tiles no longer each carry their own hue. Four colours across four
     numbers implied four kinds of thing; they are all just counts, and the one
     accent marks the icon. */
  const kpis = [
    {
      title: t("ui.inboundVolume"),
      value: shown(metrics?.inbound_24h),
      subtext: t("ui.messageVolume"),
      icon: Inbox,
    },
    {
      title: t("ui.outboundDelivered"),
      value: shown(metrics?.outbound_24h),
      subtext: t("ui.daneVerified"),
      icon: Send,
    },
    {
      title: t("ui.queueDepth"),
      value: shown(metrics?.queue_depth),
      subtext: t("ui.postgresRunner"),
      icon: Layers,
    },
    {
      title: t("ui.diskUsed"),
      value: shown(metrics?.storage_used_bytes, formatBytes),
      subtext: metrics
        ? `${t("ui.storageQuota")}: ${formatBytes(metrics.storage_quota_bytes)}`
        : t("ui.localDisk"),
      icon: HardDrive,
    },
  ];

  const panel = "flex flex-col gap-3.5 rounded-2xl bg-dark-panel p-[18px] shadow-edge";

  return (
    <div className="mx-auto flex w-full max-w-[1060px] flex-col gap-5 px-5 pb-11 pt-7 sm:px-8">
      {/* Header Bar */}
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-[25px] font-medium leading-tight text-slate-100">
            {t("admin.dashboardTitle")}
          </h1>
          <p className="mt-1.5 text-[13.5px] text-slate-400">{t("admin.dashboardSubtitle")}</p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          {/* Reports the count it can actually see. The old badge asserted
              "PREFLIGHT: HEALTHY" from a field the endpoint never sends, so it
              said healthy unconditionally. The real checks live on the
              security screen. */}
          {metrics && (
            <Badge variant={metrics.domains_verified === metrics.domains_total ? "success" : "warning"}>
              <Zap className="h-3.5 w-3.5 flex-none" />
              {t("ui.domainsVerified")}: {metrics.domains_verified}/{metrics.domains_total}
            </Badge>
          )}
          <Button variant="secondary" size="sm" onClick={fetchMetrics} disabled={loading}>
            <RefreshCw className={`h-3.5 w-3.5 ${loading ? "animate-spin" : ""}`} />
            <span>{t("common.refresh")}</span>
          </Button>
        </div>
      </div>

      {/* KPI Grid */}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {kpis.map((kpi, idx) => {
          const Icon = kpi.icon;
          return (
            <motion.div
              key={idx}
              initial={{ opacity: 0, y: 15 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.3, delay: idx * 0.05 }}
              className="flex flex-col gap-2.5 rounded-2xl bg-dark-panel p-4 shadow-edge"
            >
              <div className="flex items-center gap-2 text-slate-400">
                <Icon className="h-4 w-4 flex-none text-indigo-500" />
                <span className="text-[12.5px] leading-snug">{kpi.title}</span>
              </div>
              <div className="text-[30px] font-medium leading-none tracking-[-0.02em] tabular-nums text-slate-100">
                {kpi.value}
              </div>
              <div className="text-[11.5px] leading-snug text-slate-400">{kpi.subtext}</div>
            </motion.div>
          );
        })}
      </div>

      {/*
        A single card of figures that are genuinely queried.

        What stood here were two cards of decoration: a "core services" panel
        that listed SMTP, IMAP, Rspamd and the cert watcher with a hardcoded
        "Online"/"4/4 active" and a pulsing green dot - nothing was ever polled,
        so it stayed green through any outage - and a domain panel pinned to
        "example.com" with "13 RECORDS VERIFIED" and a "last checked: just now"
        that was a literal string. Container liveness is now covered by the
        compose healthchecks, and the per-domain DNS state lives on the domains
        screen, which reads it from the database.
      */}
      <section className={panel}>
        <div className="flex items-center gap-2">
          <Globe className="h-[17px] w-[17px] flex-none text-indigo-500" />
          <h2 className="text-[17px] font-medium leading-tight text-slate-100">
            {t("ui.domainVerification")}
          </h2>
        </div>
        {metrics ? (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <div className="flex flex-col gap-1.5 rounded-xl bg-dark-card p-3.5 shadow-edge">
              <div className="flex items-center gap-1.5 text-xs text-slate-400">
                <ShieldCheck className="h-3.5 w-3.5 flex-none text-indigo-500" />
                {t("ui.domainsVerified")}
              </div>
              <div className="text-2xl font-medium tabular-nums text-slate-100">
                {metrics.domains_verified}
                <span className="text-base text-slate-500"> / {metrics.domains_total}</span>
              </div>
            </div>
            <div className="flex flex-col gap-1.5 rounded-xl bg-dark-card p-3.5 shadow-edge">
              <div className="flex items-center gap-1.5 text-xs text-slate-400">
                <Server className="h-3.5 w-3.5 flex-none text-indigo-500" />
                {t("ui.mailboxesActive")}
              </div>
              <div className="text-2xl font-medium tabular-nums text-slate-100">
                {metrics.mailboxes_active}
              </div>
            </div>
            <div className="flex flex-col gap-1.5 rounded-xl bg-dark-card p-3.5 shadow-edge">
              <div className="flex items-center gap-1.5 text-xs text-slate-400">
                <Send className="h-3.5 w-3.5 flex-none text-indigo-500" />
                {t("ui.bounceRate")}
              </div>
              <div className="text-2xl font-medium tabular-nums text-slate-100">
                {(metrics.bounce_rate * 100).toFixed(1)}%
              </div>
            </div>
          </div>
        ) : (
          <div className="p-6 text-center text-[13px] text-slate-400">{t("ui.noData")}</div>
        )}
      </section>
    </div>
  );
}
