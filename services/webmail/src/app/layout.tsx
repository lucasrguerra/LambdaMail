import "./globals.css";
import React from "react";

export const metadata = {
  title: "LambdaMail - Secure Self-Hosted Webmail & Admin Console",
  description: "Modern, open-source email server web interface with 2FA TOTP and surface isolation.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className="dark">
      <body className="bg-slate-950 text-slate-100 min-h-screen font-sans antialiased">
        {children}
      </body>
    </html>
  );
}
