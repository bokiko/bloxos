"use client";

// Sign-in. Renders outside AppShell, so it carries the Monoform canvas itself
// — the same void ground, graphite panel, hairline border and 36px controls
// the authenticated product uses. No gradient wash, no grid wallpaper, no
// glass: the first screen has to look like the rest of the product.

import { useState, FormEvent, useEffect, useTransition } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/contexts/AuthContext";
import { HUB_URL } from "@/lib/session";
import { Input } from "@/components/ui/input";
import { BrandedHeader } from "@/components/BrandedHeader";
import { BrandingPlate } from "@/components/BrandingPlate";
import { useBranding } from "@/contexts/BrandingContext";
import { MF_INPUT, MF_LABEL } from "@/lib/monoform-classes";

export default function LoginPage() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [checkingSetup, startSetupCheck] = useTransition();
  const { login, isAuthenticated } = useAuth();
  const { branding } = useBranding();
  const router = useRouter();

  useEffect(() => {
    if (isAuthenticated) {
      router.replace("/");
    }
  }, [isAuthenticated, router]);

  useEffect(() => {
    if (isAuthenticated) {
      return;
    }
    let active = true;
    startSetupCheck(async () => {
      try {
        const res = await fetch(`${HUB_URL}/api/setup/status`);
        if (!res.ok || !active) return;
        const data = await res.json();
        if (active && data?.needs_setup) {
          router.replace("/setup");
        }
      } catch {
        // ignore setup status failures and keep login usable
      }
    });
    return () => {
      active = false;
    };
  }, [isAuthenticated, router]);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    setLoading(true);
    const ok = await login(username, password);
    setLoading(false);
    if (ok) {
      router.push("/");
    } else {
      setError("Invalid username or password");
    }
  };

  return (
    <div className="min-h-screen bg-surface-sunken flex items-center justify-center px-4 py-12">
      <div className="w-full max-w-sm">
        <div className="mb-9 flex flex-col items-center">
          <BrandingPlate>
            <BrandedHeader size="expanded" />
          </BrandingPlate>
          {branding.welcome_message && (
            <p className="mt-4 max-w-md whitespace-pre-line text-center text-[11px] leading-5 text-text-tertiary">
              {branding.welcome_message}
            </p>
          )}
        </div>

        <div className="mf-panel px-6 py-6">
          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label htmlFor="login-username" className={MF_LABEL}>Username</label>
              <Input
                id="login-username"
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className={`${MF_INPUT} w-full`}
                placeholder="admin"
                autoFocus
                autoComplete="username"
                disabled={checkingSetup}
              />
            </div>
            <div>
              <label htmlFor="login-password" className={MF_LABEL}>Password</label>
              <Input
                id="login-password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className={`${MF_INPUT} w-full`}
                placeholder="password"
                autoComplete="current-password"
                disabled={checkingSetup}
              />
            </div>

            {error && (
              <p
                role="alert"
                className="rounded-lg border border-status-critical/40 bg-status-critical-tint px-3 py-2 text-xs text-status-critical"
              >
                {error}
              </p>
            )}

            <button
              type="submit"
              disabled={loading || checkingSetup || !username || !password}
              className="mf-action inline-flex w-full items-center justify-center gap-2 disabled:opacity-50"
            >
              {checkingSetup ? (
                <>
                  <Spinner />
                  Checking setup…
                </>
              ) : loading ? (
                <>
                  <Spinner />
                  Signing in…
                </>
              ) : (
                "Sign in"
              )}
            </button>
          </form>
        </div>

        <p className="mt-6 text-center text-[10px] text-text-disabled">
          Secure fleet monitoring and management
        </p>
      </div>
    </div>
  );
}

function Spinner() {
  return (
    <span
      className="w-3 h-3 rounded-full border-2 border-white/40 border-t-white animate-spin"
      aria-hidden
    />
  );
}
