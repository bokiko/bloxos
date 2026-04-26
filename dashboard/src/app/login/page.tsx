"use client";

import { useState, FormEvent, useEffect, useTransition } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/contexts/AuthContext";
import { HUB_URL } from "@/lib/session";
import { motion } from "framer-motion";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";

export default function LoginPage() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [checkingSetup, startSetupCheck] = useTransition();
  const { login, isAuthenticated } = useAuth();
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
    <div className="min-h-screen bg-blox-bg flex items-center justify-center relative overflow-hidden">
      {/* Background pattern */}
      <div className="absolute inset-0 bg-[radial-gradient(ellipse_at_center,_var(--color-blox-blue)_0%,_transparent_70%)] opacity-[0.03]" />
      <div className="absolute inset-0 bg-[linear-gradient(rgba(59,130,246,0.03)_1px,transparent_1px),linear-gradient(90deg,rgba(59,130,246,0.03)_1px,transparent_1px)] bg-[size:64px_64px]" />

      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.3, ease: "easeOut" }}
        className="w-full max-w-sm relative z-10"
      >
        <div className="text-center mb-8">
          <h1 className="text-3xl font-bold tracking-tight">
            <span className="text-blox-blue">Blox</span>
            <span className="text-blox-text">OS</span>
          </h1>
          <p className="text-sm text-blox-muted mt-2">Fleet Management Dashboard</p>
        </div>

        <Card className="bg-blox-card/80 backdrop-blur-sm border-blox-border ring-0 shadow-2xl shadow-black/40">
          <CardContent className="pt-6 px-6 pb-6">
            <form onSubmit={handleSubmit} className="space-y-4">
              <div>
                <label className="block text-xs text-blox-muted mb-1.5 font-medium">Username</label>
                <Input
                  type="text"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  className="bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50 h-9 text-sm"
                  placeholder="admin"
                  autoFocus
                  autoComplete="username"
                  disabled={checkingSetup}
                />
              </div>
              <div>
                <label className="block text-xs text-blox-muted mb-1.5 font-medium">Password</label>
                <Input
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  className="bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50 h-9 text-sm"
                  placeholder="password"
                  autoComplete="current-password"
                  disabled={checkingSetup}
                />
              </div>

              {error && (
                <motion.p
                  initial={{ opacity: 0, y: -4 }}
                  animate={{ opacity: 1, y: 0 }}
                  className="text-xs text-blox-red"
                >
                  {error}
                </motion.p>
              )}

              <Button
                type="submit"
                disabled={loading || checkingSetup || !username || !password}
                className="w-full bg-blox-blue hover:bg-blox-blue/90 text-white h-9 text-sm font-medium gap-2"
              >
                {checkingSetup ? (
                  <>
                    <span className="w-3 h-3 border-2 border-white/40 border-t-white rounded-full animate-spin" />
                    Checking setup…
                  </>
                ) : loading ? (
                  <>
                    <span className="w-3 h-3 border-2 border-white/40 border-t-white rounded-full animate-spin" />
                    Signing in…
                  </>
                ) : (
                  "Sign In"
                )}
              </Button>
            </form>
          </CardContent>
        </Card>

        <p className="text-center text-[10px] text-blox-muted/40 mt-6">
          Secure fleet monitoring and management
        </p>
      </motion.div>
    </div>
  );
}
