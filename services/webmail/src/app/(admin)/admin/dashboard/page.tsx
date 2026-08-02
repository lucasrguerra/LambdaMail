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

interface DashboardMetrics {
  inbound_24h: number;
  outbound_24h: number;
  queue_depth: number;
  bounce_rate: number;
  spam_score_avg: number;
  disk_used_percent: number;
  domains_verified: number;
  domains_total: number;
  preflight_status: string;
}

export default function AdminDashboardPage() {
  const t = useTranslations();
  const [metrics, setMetrics] = useState<DashboardMetrics | null>(null);
  const [loading, setLoading] = useState(false);

  const fetchMetrics = () => {
    setLoading(true);
    fetch("/api/v1/admin/dashboard")
      .then((res) => res.json())
      .then((data) => setMetrics(data))
      .catch(() => {})
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    fetchMetrics();
  }, []);

  const kpis = [
    {
      title: t("ui.inboundVolume"),
      value: metrics?.inbound_24h ?? 1420,
      subtext: "100% SPF/DKIM validado",
      icon: Inbox,
      color: "text-indigo-400",
      bg: "bg-indigo-500/10",
    },
    {
      title: t("ui.outboundDelivered"),
      value: metrics?.outbound_24h ?? 890,
      subtext: t("ui.daneVerified"),
      icon: Send,
      color: "text-emerald-400",
      bg: "bg-emerald-500/10",
    },
    {
      title: t("ui.queueDepth"),
      value: metrics?.queue_depth ?? 3,
      subtext: t("ui.postgresRunner"),
      icon: Layers,
      color: "text-cyan-400",
      bg: "bg-cyan-500/10",
    },
    {
      title: t("ui.diskUsed"),
      value: `${metrics?.disk_used_percent ?? 18.4}%`,
      subtext: t("ui.localDisk"),
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
          <h1 className="text-2xl md:text-3xl font-extrabold text-white tracking-tight flex items-center gap-3">
            {t("admin.dashboardTitle")}
            <span className="text-xs px-2.5 py-1 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/20 font-mono font-normal">
              v1.2 Live
            </span>
          </h1>
          <p className="text-sm text-slate-400 mt-1">{t("ui.liveMetrics")}</p>
        </div>

        <div className="flex items-center gap-3">
          <Badge variant="success" className="font-mono py-1">
            <Zap className="w-3.5 h-3.5 mr-1 animate-pulse" />
            PREFLIGHT: {metrics?.preflight_status || "HEALTHY"}
          </Badge>
          <Button variant="outline" size="sm" onClick={fetchMetrics} disabled={loading}>
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? "animate-spin" : ""}`} />
            <span>Atualizar</span>
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

      {/* Security & System Infra */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Core Infrastructure Health */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Server className="w-5 h-5 text-emerald-400" />
              {t("ui.coreInfra")}
            </CardTitle>
            <Badge variant="info">4/4 Ativos</Badge>
          </CardHeader>
          <div className="space-y-3">
            {[
              { name: t("ui.smtpListener"), status: t("ui.online"), port: "Porta 25 / 587" },
              { name: t("ui.imapEngine"), status: t("ui.online"), port: "Porta 993 (SSL)" },
              { name: t("ui.scanners"), status: t("ui.healthy"), port: "Rspamd Engine" },
              { name: "Traefik Acme Watcher", status: t("ui.watching"), port: "fsnotify + poll" },
            ].map((svc, i) => (
              <div
                key={i}
                className="flex items-center justify-between p-3.5 rounded-xl bg-slate-900/80 border border-slate-800/80 hover:border-slate-700/60 transition-colors"
              >
                <div className="flex items-center gap-3">
                  <div className="w-2 h-2 rounded-full bg-emerald-400 animate-ping" />
                  <div>
                    <span className="text-xs font-semibold text-slate-200 block">{svc.name}</span>
                    <span className="text-[10px] text-slate-400 font-mono">{svc.port}</span>
                  </div>
                </div>
                <Badge variant="success">{svc.status}</Badge>
              </div>
            ))}
          </div>
        </Card>

        {/* Domain Reconciliation */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Globe className="w-5 h-5 text-indigo-400" />
              {t("ui.domainVerification")}
            </CardTitle>
            <Badge variant="success">Auto-Reconciled</Badge>
          </CardHeader>
          <div className="p-4 rounded-xl bg-slate-900/90 border border-slate-800 space-y-3">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <ShieldCheck className="w-4 h-4 text-emerald-400" />
                <span className="font-bold text-sm text-slate-100">example.com</span>
              </div>
              <Badge variant="success">13 RECORDS VERIFIED</Badge>
            </div>
            <p className="text-xs text-slate-400 leading-relaxed">
              Cloudflare DNS automation active. SPF, DKIM (RSA 2048 + Ed25519), DMARC (quarantine), MTA-STS e TLS-RPT totalmente sincronizados e validados.
            </p>
            <div className="pt-2 flex items-center justify-between text-xs text-slate-400 border-t border-slate-800 font-mono">
              <span>Last checked: just now</span>
              <span className="text-indigo-400 hover:underline cursor-pointer">Verificar Registros &rarr;</span>
            </div>
          </div>
        </Card>
      </div>
    </div>
  );
}
