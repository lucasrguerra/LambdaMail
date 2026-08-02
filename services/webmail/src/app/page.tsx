import Link from "next/link";

export default function LandingPage() {
  return (
    <div className="min-h-screen flex flex-col justify-center items-center px-4 bg-gradient-to-b from-slate-950 via-slate-900 to-slate-950">
      <div className="text-center max-w-2xl mx-auto mb-12">
        <div className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-indigo-500/10 border border-indigo-500/20 text-indigo-400 text-sm font-medium mb-6">
          <span className="w-2 h-2 rounded-full bg-indigo-400 animate-pulse"></span>
          LambdaMail v2.0 - Phase F4 Complete
        </div>
        <h1 className="text-4xl md:text-5xl font-extrabold tracking-tight bg-gradient-to-r from-white via-slate-200 to-slate-400 bg-clip-text text-transparent mb-4">
          Self-Hosted Mail Infrastructure
        </h1>
        <p className="text-slate-400 text-lg">
          Integrated Webmail & Administration Platform featuring strict surface isolation, RFC 6238 TOTP authentication, and 13-record DNS automation.
        </p>
      </div>

      <div className="grid md:grid-cols-2 gap-8 max-w-4xl w-full">
        {/* Webmail Surface Card */}
        <div className="glass-panel rounded-2xl p-8 hover:border-indigo-500/40 transition-all group flex flex-col justify-between">
          <div>
            <div className="w-12 h-12 rounded-xl bg-indigo-500/20 border border-indigo-500/30 flex items-center justify-center text-indigo-400 text-2xl font-bold mb-6 group-hover:scale-110 transition-transform">
              @
            </div>
            <h2 className="text-2xl font-bold text-white mb-2">Webmail Surface</h2>
            <p className="text-slate-400 text-sm mb-6">
              Daily email workspace. Access your inbox, compose messages with sandboxed HTML rendering, manage Sieve filters, and 2FA settings.
            </p>
            <div className="text-xs text-slate-500 font-mono space-y-1 mb-8 bg-slate-900/60 p-3 rounded-lg border border-slate-800">
              <div>Path: /user/*</div>
              <div>Cookie: lm_user_session (Path=/user)</div>
              <div>Audience: lambdamail:user</div>
            </div>
          </div>
          <Link
            href="/user/login"
            className="w-full py-3 px-4 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white font-medium text-center transition-colors shadow-lg shadow-indigo-600/20"
          >
            Access Webmail Portal &rarr;
          </Link>
        </div>

        {/* Admin Console Card */}
        <div className="glass-panel rounded-2xl p-8 hover:border-emerald-500/40 transition-all group flex flex-col justify-between">
          <div>
            <div className="w-12 h-12 rounded-xl bg-emerald-500/20 border border-emerald-500/30 flex items-center justify-center text-emerald-400 text-2xl font-bold mb-6 group-hover:scale-110 transition-transform">
              &#9881;
            </div>
            <h2 className="text-2xl font-bold text-white mb-2">Management Console</h2>
            <p className="text-slate-400 text-sm mb-6">
              Stalwart-parity admin console. Manage domains, 13 DNS records, mailboxes, DKIM rotation, outbound queue, DMARC reports, and preflight checks.
            </p>
            <div className="text-xs text-slate-500 font-mono space-y-1 mb-8 bg-slate-900/60 p-3 rounded-lg border border-slate-800">
              <div>Path: /admin/*</div>
              <div>Cookie: lm_admin_session (Path=/admin)</div>
              <div>Audience: lambdamail:admin (MFA Mandatory)</div>
            </div>
          </div>
          <Link
            href="/admin/login"
            className="w-full py-3 px-4 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white font-medium text-center transition-colors shadow-lg shadow-emerald-600/20"
          >
            Access Admin Console &rarr;
          </Link>
        </div>
      </div>
    </div>
  );
}
