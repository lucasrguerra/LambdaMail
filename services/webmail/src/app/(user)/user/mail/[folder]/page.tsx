"use client";

import React, { useState } from "react";

interface SampleMessage {
  id: string;
  sender: string;
  senderEmail: string;
  subject: string;
  snippet: string;
  date: string;
  unread: boolean;
  hasAttachment: boolean;
  htmlContent: string;
  spf: string;
  dkim: string;
  dmarc: string;
}

const MOCK_MESSAGES: SampleMessage[] = [
  {
    id: "msg-1",
    sender: "Security Operations",
    senderEmail: "security@lambdamail.org",
    subject: "Welcome to LambdaMail v2.0 - Account Provisioned",
    snippet: "Your LambdaMail account is active with strict SPF, DKIM, and DMARC verification.",
    date: "10:42 AM",
    unread: true,
    hasAttachment: false,
    spf: "pass",
    dkim: "pass",
    dmarc: "pass",
    htmlContent: `
      <div style="font-family: sans-serif; padding: 20px; color: #333;">
        <h2>Welcome to LambdaMail v2.0</h2>
        <p>Your secure self-hosted email box is active.</p>
        <ul>
          <li><strong>Surface Isolation:</strong> Cookie Path=/user enforced</li>
          <li><strong>2FA TOTP:</strong> Active RFC 6238 support</li>
          <li><strong>Mail Security:</strong> 13 DNS records reconciled via Cloudflare</li>
        </ul>
        <img src="https://example.com/pixel.png" alt="Remote pixel test" style="display:none;" />
      </div>
    `,
  },
  {
    id: "msg-2",
    sender: "GitHub Notifications",
    senderEmail: "notifications@github.com",
    subject: "[LambdaMail/LambdaMail] Pull Request #20 Merged",
    snippet: "Feature branch feature/20-f4-webmail-admin-mfa-i18n merged successfully.",
    date: "Yesterday",
    unread: false,
    hasAttachment: true,
    spf: "pass",
    dkim: "pass",
    dmarc: "pass",
    htmlContent: `
      <div style="font-family: sans-serif; padding: 20px; color: #333;">
        <h3>Pull Request #20 Merged into main</h3>
        <p>Phase F4 (Webmail, Admin Console, 2FA TOTP, and i18n) is approved!</p>
      </div>
    `,
  },
];

export default function MailFolderPage() {
  const [selectedMsg, setSelectedMsg] = useState<SampleMessage | null>(MOCK_MESSAGES[0]);
  const [loadRemoteImages, setLoadRemoteImages] = useState(false);
  const [searchQuery, setSearchQuery] = useState("");

  return (
    <div className="flex-1 flex overflow-hidden">
      {/* Messages List Column */}
      <div className="w-96 border-r border-slate-800 flex flex-col bg-slate-900/30">
        {/* Search Bar */}
        <div className="p-3 border-b border-slate-800">
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Search email (e.g. from:admin has:attachment)..."
            className="w-full px-3 py-2 text-xs rounded-lg bg-slate-900 border border-slate-800 text-white placeholder-slate-500 focus:outline-none focus:border-indigo-500"
          />
        </div>

        {/* Message Items List */}
        <div className="flex-1 overflow-y-auto divide-y divide-slate-800/60">
          {MOCK_MESSAGES.map((msg) => (
            <button
              key={msg.id}
              onClick={() => {
                setSelectedMsg(msg);
                setLoadRemoteImages(false);
              }}
              className={`w-full text-left p-4 transition-colors block ${
                selectedMsg?.id === msg.id ? "bg-indigo-600/10 border-l-2 border-indigo-500" : "hover:bg-slate-800/40"
              }`}
            >
              <div className="flex items-center justify-between mb-1">
                <span className={`text-xs font-semibold ${msg.unread ? "text-white" : "text-slate-300"}`}>
                  {msg.sender}
                </span>
                <span className="text-[10px] text-slate-500">{msg.date}</span>
              </div>
              <div className={`text-xs truncate mb-1 ${msg.unread ? "font-bold text-slate-100" : "text-slate-300"}`}>
                {msg.subject}
              </div>
              <div className="text-[11px] text-slate-400 truncate">{msg.snippet}</div>
            </button>
          ))}
        </div>
      </div>

      {/* Message Reader Column */}
      <div className="flex-1 flex flex-col bg-slate-950 overflow-y-auto p-6">
        {selectedMsg ? (
          <div className="max-w-3xl w-full mx-auto">
            {/* Message Header */}
            <div className="border-b border-slate-800 pb-6 mb-6">
              <div className="flex items-center justify-between mb-4">
                <h1 className="text-xl font-bold text-white">{selectedMsg.subject}</h1>
                <div className="flex items-center gap-2">
                  <span className="badge-verified px-2 py-0.5 rounded text-[10px] font-mono">
                    SPF:{selectedMsg.spf}
                  </span>
                  <span className="badge-verified px-2 py-0.5 rounded text-[10px] font-mono">
                    DKIM:{selectedMsg.dkim}
                  </span>
                  <span className="badge-verified px-2 py-0.5 rounded text-[10px] font-mono">
                    DMARC:{selectedMsg.dmarc}
                  </span>
                </div>
              </div>

              <div className="flex items-center justify-between text-xs text-slate-400">
                <div>
                  <span className="font-semibold text-slate-200">{selectedMsg.sender}</span> &lt;{selectedMsg.senderEmail}&gt;
                </div>
                <div>{selectedMsg.date}</div>
              </div>
            </div>

            {/* Remote Images Banner */}
            <div className="mb-4 p-3 rounded-lg bg-amber-500/10 border border-amber-500/20 flex items-center justify-between text-xs text-amber-300">
              <span>Remote images are blocked by default for security (anti-tracking pixel protection).</span>
              <button
                onClick={() => setLoadRemoteImages(!loadRemoteImages)}
                className="px-3 py-1 rounded bg-amber-500/20 hover:bg-amber-500/30 text-amber-200 font-medium transition-colors"
              >
                {loadRemoteImages ? "Images Loaded" : "Load Remote Images"}
              </button>
            </div>

            {/* Isolated HTML Sandboxed Frame Container */}
            <div className="bg-white rounded-xl overflow-hidden shadow-xl border border-slate-800 min-h-[300px]">
              <iframe
                title="Message Reader Body"
                srcDoc={selectedMsg.htmlContent}
                sandbox="allow-popups allow-popups-to-escape-sandbox"
                className="w-full h-96 border-0"
              />
            </div>
          </div>
        ) : (
          <div className="flex-1 flex items-center justify-center text-slate-500 text-sm">
            Select a message to display contents
          </div>
        )}
      </div>
    </div>
  );
}
