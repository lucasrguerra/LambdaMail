"use client";

import React, { useEffect, useState } from "react";
import { motion } from "framer-motion";
import {
  Inbox,
  Send,
  Layers,
  HardDrive,
  CheckCircle2,
  Server,
  ShieldCheck,
  Globe,
  RefreshCw,
  Zap,
} from "lucide-react";
import { useTranslations } from "../../../../i18n/provider";
import { Card, CardHeader, CardTitle } from "../../../../components/ui/Card";
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

  const kpis = [
    {
      title: t("ui.inboundVolume"),
      value: shown(metrics?.inbound_24h),
      subtext: t("ui.messageVolume"),
      icon: Inbox,
      color: "text-indigo-400",
      bg: "bg-indigo-500/10",
    },
    {
      title: t("ui.outboundDelivered"),
      value: shown(metrics?.outbound_24h),
      subtext: t("ui.daneVerified"),
      icon: Send,
      color: "text-emerald-400",
      bg: "bg-emerald-500/10",
    },
    {
      title: t("ui.queueDepth"),
      value: shown(metrics?.queue_depth),
      subtext: t("ui.postgresRunner"),
      icon: Layers,
      color: "text-cyan-400",
      bg: "bg-cyan-500/10",
    },
    {
      title: t("ui.diskUsed"),
      value: shown(metrics?.storage_used_bytes, formatBytes),
      subtext: metrics
        ? `${t("ui.storageQuota")}: ${formatBytes(metrics.storage_quota_bytes)}`
        : t("ui.localDisk"),
      icon: HardDrive,
      color: "text-amber-400",
      bg: "bg-amber-500/10",
    },
  ];

  return (
    <div className="p-6 md:p-8 space-y-8 max-w-7xl mx-auto">
      {/* Header Bar */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl md:text-3xl font-extrabold text-white tracking-tight">
            {t("admin.dashboardTitle")}
          </h1>
          <p className="text-sm text-slate-400 mt-1">{t("admin.dashboardSubtitle")}</p>
        </div>

        <div className="flex items-center gap-3">
          {/* Reports the count it can actually see. The old badge asserted
              "PREFLIGHT: HEALTHY" from a field the endpoint never sends, so it
              said healthy unconditionally. The real checks live on the
              security screen. */}
          {metrics && (
            <Badge variant={metrics.domains_verified === metrics.domains_total ? "success" : "warning"}>
              <Zap className="w-3.5 h-3.5 mr-1" />
              {t("ui.domainsVerified")}: {metrics.domains_verified}/{metrics.domains_total}
            </Badge>
          )}
          <Button variant="outline" size="sm" onClick={fetchMetrics} disabled={loading}>
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? "animate-spin" : ""}`} />
            <span>{t("common.refresh")}</span>
          </Button>
        </div>
      </div>

      {/* KPI Grid */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-5">
        {kpis.map((kpi, idx) => {
          const Icon = kpi.icon;
          return (
            <motion.div
              key={idx}
              initial={{ opacity: 0, y: 15 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.3, delay: idx * 0.05 }}
            >
              <Card hoverable className="relative overflow-hidden">
                <div className="flex items-center justify-between">
                  <span className="text-xs font-medium text-slate-400">{kpi.title}</span>
                  <div className={`w-9 h-9 rounded-xl ${kpi.bg} flex items-center justify-center`}>
                    <Icon className={`w-5 h-5 ${kpi.color}`} />
                  </div>
                </div>
                <div className="text-3xl font-black text-white mt-3 tracking-tight">
                  {kpi.value}
                </div>
                <div className="text-xs text-slate-400 mt-2 flex items-center gap-1 font-mono">
                  <CheckCircle2 className="w-3.5 h-3.5 text-emerald-400 inline" />
                  {kpi.subtext}
                </div>
              </Card>
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
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Globe className="w-5 h-5 text-indigo-400" />
            {t("ui.domainVerification")}
          </CardTitle>
        </CardHeader>
        {metrics ? (
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
            <div className="p-4 rounded-xl bg-slate-900/90 border border-slate-800">
              <div className="text-xs text-slate-400 mb-1.5 flex items-center gap-1.5">
                <ShieldCheck className="w-3.5 h-3.5 text-emerald-400" />
                {t("ui.domainsVerified")}
              </div>
              <div className="text-2xl font-bold text-white">
                {metrics.domains_verified}
                <span className="text-slate-500 text-base"> / {metrics.domains_total}</span>
              </div>
            </div>
            <div className="p-4 rounded-xl bg-slate-900/90 border border-slate-800">
              <div className="text-xs text-slate-400 mb-1.5 flex items-center gap-1.5">
                <Server className="w-3.5 h-3.5 text-indigo-400" />
                {t("ui.mailboxesActive")}
              </div>
              <div className="text-2xl font-bold text-white">{metrics.mailboxes_active}</div>
            </div>
            <div className="p-4 rounded-xl bg-slate-900/90 border border-slate-800">
              <div className="text-xs text-slate-400 mb-1.5 flex items-center gap-1.5">
                <Send className="w-3.5 h-3.5 text-amber-400" />
                {t("ui.bounceRate")}
              </div>
              <div className="text-2xl font-bold text-white">
                {(metrics.bounce_rate * 100).toFixed(1)}%
              </div>
            </div>
          </div>
        ) : (
          <div className="p-6 text-center text-xs text-slate-500">{t("ui.noData")}</div>
        )}
      </Card>
    </div>
  );
}
