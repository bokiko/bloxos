"use client";

import { FormEvent, useEffect, useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { motion } from "framer-motion";
import { ShieldCheck, KeyRound, LockKeyhole, Sparkles } from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/contexts/AuthContext";
import { HUB_URL } from "@/lib/session";

type SetupState = "checking" | "ready" | "error" | "complete";

export default function SetupPage() {
  const [setupState, setSetupState] = useState<SetupState>("checking");
  const [setupCheckNonce, setSetupCheckNonce] = useState(0);
  const [setupToken, setSetupToken] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [pin, setPin] = useState("");
  const [confirmPin, setConfirmPin] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [createdUsername, setCreatedUsername] = useState("");
  const [navigating, startNavigation] = useTransition();
  const router = useRouter();
  const { login, isAuthenticated } = useAuth();

  useEffect(() => {
    if (isAuthenticated) {
      router.replace("/");
    }
  }, [isAuthenticated, router]);

  useEffect(() => {
    let active = true;
    const checkSetup = async () => {
      setSetupState("checking");
      setError("");
      try {
        const res = await fetch(`${HUB_URL}/api/setup/status`);
        if (!res.ok) {
          throw new Error("Failed to check setup status.");
        }
        const data = await res.json();
        if (!active) return;
        if (data?.needs_setup) {
          setSetupState("ready");
        } else {
          startNavigation(() => {
            router.replace(isAuthenticated ? "/" : "/login");
          });
        }
      } catch {
        if (active) {
          setError("Unable to reach the setup service. Check the hub and try again.");
          setSetupState("error");
        }
      }
    };
    void checkSetup();
    return () => {
      active = false;
    };
  }, [isAuthenticated, router, setupCheckNonce]);

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault();

    const trimmedToken = setupToken.trim();
    const trimmedUsername = username.trim();

    if (!trimmedToken || !trimmedUsername || !password || !pin) {
      setError("Setup token, username, password, and PIN are required.");
      return;
    }
    if (password.length < 8) {
      setError("Password must be at least 8 characters.");
      return;
    }
    if (password !== confirmPassword) {
      setError("Passwords do not match.");
      return;
    }
    if (!/^\d{4,}$/.test(pin)) {
      setError("PIN must be at least 4 digits.");
      return;
    }
    if (pin !== confirmPin) {
      setError("PIN values do not match.");
      return;
    }

    setLoading(true);
    setError("");

    try {
      const setupRes = await fetch(`${HUB_URL}/api/setup`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          setup_token: trimmedToken,
          username: trimmedUsername,
          password,
          pin,
        }),
      });

      const setupData = await setupRes.json().catch(() => null);
      if (!setupRes.ok) {
        throw new Error(setupData?.error || `Setup failed (${setupRes.status})`);
      }

      setCreatedUsername(setupData?.username || trimmedUsername);
      setSetupState("complete");

      const loggedIn = await login(trimmedUsername, password);
      if (loggedIn) {
        startNavigation(() => {
          router.replace("/");
        });
      } else {
        setError("Setup succeeded, but automatic sign-in failed. Use the button below to go to login.");
      }
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Setup failed.");
      setSetupState("ready");
    } finally {
      setLoading(false);
    }
  };

  const disabled = loading || setupState === "checking" || navigating;

  return (
    <div className="min-h-screen bg-blox-bg relative overflow-hidden">
      <div className="absolute inset-0 bg-[radial-gradient(circle_at_top_left,_rgba(59,130,246,0.12),_transparent_36%),radial-gradient(circle_at_bottom_right,_rgba(34,197,94,0.08),_transparent_28%)]" />
      <div className="absolute inset-0 bg-[linear-gradient(rgba(59,130,246,0.03)_1px,transparent_1px),linear-gradient(90deg,rgba(59,130,246,0.03)_1px,transparent_1px)] bg-[size:72px_72px]" />
      <div className="absolute inset-y-0 right-0 w-1/3 bg-[radial-gradient(circle_at_center,_rgba(255,255,255,0.04),_transparent_60%)]" />

      <div className="relative z-10 min-h-screen grid lg:grid-cols-[1.1fr_0.9fr]">
        <section className="hidden lg:flex flex-col justify-between px-10 py-12 border-r border-blox-border/60">
          <div>
            <div className="inline-flex items-center gap-2 rounded-full border border-blox-blue/20 bg-blox-blue/8 px-3 py-1 text-[11px] uppercase tracking-[0.28em] text-blox-blue">
              <Sparkles className="w-3.5 h-3.5" />
              First Boot
            </div>
            <h1 className="mt-8 max-w-xl text-5xl font-semibold tracking-tight text-blox-text">
              Turn a blank install into a locked-down control plane.
            </h1>
            <p className="mt-5 max-w-lg text-sm leading-7 text-blox-muted">
              This one-time setup flow creates your first admin, seals the default credential path, and sets the terminal PIN before the dashboard ever opens.
            </p>
          </div>

          <div className="space-y-4">
            {[
              {
                icon: ShieldCheck,
                title: "Bootstrap proof",
                body: "Use the setup token from the server to prove you control the host before the first admin exists.",
              },
              {
                icon: KeyRound,
                title: "Rotated from minute one",
                body: "The admin account and terminal PIN are stored as already-changed credentials, not shipped defaults.",
              },
              {
                icon: LockKeyhole,
                title: "One path, one time",
                body: "Once setup completes, the backend closes this path and the dashboard falls back to the normal login flow.",
              },
            ].map(({ icon: Icon, title, body }) => (
              <motion.div
                key={title}
                initial={{ opacity: 0, y: 18 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ duration: 0.45 }}
                className="rounded-2xl border border-blox-border/70 bg-blox-card/55 px-5 py-4 shadow-lg shadow-black/15 backdrop-blur-sm"
              >
                <div className="flex items-start gap-3">
                  <div className="mt-0.5 rounded-xl border border-blox-blue/20 bg-blox-blue/10 p-2 text-blox-blue">
                    <Icon className="w-4 h-4" />
                  </div>
                  <div>
                    <p className="text-sm font-medium text-blox-text">{title}</p>
                    <p className="mt-1 text-xs leading-6 text-blox-muted">{body}</p>
                  </div>
                </div>
              </motion.div>
            ))}
          </div>
        </section>

        <section className="flex items-center justify-center px-4 py-10 sm:px-6">
          <motion.div
            initial={{ opacity: 0, y: 24 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.45, ease: "easeOut" }}
            className="w-full max-w-xl"
          >
            <div className="mb-8 text-center lg:text-left">
              <h2 className="text-3xl font-semibold tracking-tight text-blox-text">
                <span className="text-blox-blue">Blox</span>OS Setup
              </h2>
              <p className="mt-2 text-sm text-blox-muted">
                Create the first administrator and secure terminal access.
              </p>
            </div>

            <Card className="border-blox-border/80 bg-blox-card/82 shadow-2xl shadow-black/25 backdrop-blur-md">
              <CardContent className="px-6 py-6 sm:px-7">
                {setupState === "complete" ? (
                  <div className="space-y-5 text-center">
                    <div className="mx-auto flex h-14 w-14 items-center justify-center rounded-2xl border border-emerald-500/20 bg-emerald-500/10 text-emerald-400">
                      <ShieldCheck className="w-7 h-7" />
                    </div>
                    <div>
                      <h3 className="text-xl font-semibold text-blox-text">Setup complete</h3>
                      <p className="mt-2 text-sm leading-6 text-blox-muted">
                        Admin <span className="text-blox-text font-medium">{createdUsername}</span> is ready. {navigating ? "Opening the dashboard..." : "You can sign in now."}
                      </p>
                    </div>
                    {error && (
                      <motion.p
                        initial={{ opacity: 0, y: -4 }}
                        animate={{ opacity: 1, y: 0 }}
                        className="rounded-xl border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs leading-5 text-blox-red"
                      >
                        {error}
                      </motion.p>
                    )}
                    {!navigating && (
                      <Button
                        onClick={() => router.replace("/login")}
                        className="w-full bg-blox-blue hover:bg-blox-blue/90 text-white"
                      >
                        Go to Login
                      </Button>
                    )}
                  </div>
                ) : setupState === "error" ? (
                  <div className="space-y-5 text-center">
                    <div className="mx-auto flex h-14 w-14 items-center justify-center rounded-2xl border border-red-500/20 bg-red-500/10 text-blox-red">
                      <LockKeyhole className="w-7 h-7" />
                    </div>
                    <div>
                      <h3 className="text-xl font-semibold text-blox-text">Setup service unavailable</h3>
                      <p className="mt-2 text-sm leading-6 text-blox-muted">
                        {error || "The dashboard could not verify whether first-boot setup is still required."}
                      </p>
                    </div>
                    <Button
                      onClick={() => {
                        setSetupState("checking");
                        setError("");
                        setSetupCheckNonce((value) => value + 1);
                      }}
                      className="w-full bg-blox-blue hover:bg-blox-blue/90 text-white"
                    >
                      Retry Setup Check
                    </Button>
                  </div>
                ) : (
                  <form onSubmit={handleSubmit} className="space-y-5">
                    <div className="grid gap-4 sm:grid-cols-2">
                      <div className="sm:col-span-2">
                        <label className="block text-[11px] uppercase tracking-[0.24em] text-blox-muted mb-1.5">Setup Token</label>
                        <Input
                          type="text"
                          value={setupToken}
                          onChange={(e) => setSetupToken(e.target.value)}
                          placeholder="Paste the one-time setup token"
                          className="h-10 bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50 font-mono"
                          autoComplete="off"
                          autoFocus
                          disabled={disabled}
                        />
                      </div>

                      <div className="sm:col-span-2">
                        <label className="block text-[11px] uppercase tracking-[0.24em] text-blox-muted mb-1.5">Admin Username</label>
                        <Input
                          type="text"
                          value={username}
                          onChange={(e) => setUsername(e.target.value)}
                          placeholder="admin"
                          className="h-10 bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50"
                          autoComplete="username"
                          disabled={disabled}
                        />
                      </div>

                      <div>
                        <label className="block text-[11px] uppercase tracking-[0.24em] text-blox-muted mb-1.5">Password</label>
                        <Input
                          type="password"
                          value={password}
                          onChange={(e) => setPassword(e.target.value)}
                          placeholder="Minimum 8 characters"
                          className="h-10 bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50"
                          autoComplete="new-password"
                          disabled={disabled}
                        />
                      </div>

                      <div>
                        <label className="block text-[11px] uppercase tracking-[0.24em] text-blox-muted mb-1.5">Confirm Password</label>
                        <Input
                          type="password"
                          value={confirmPassword}
                          onChange={(e) => setConfirmPassword(e.target.value)}
                          placeholder="Repeat password"
                          className="h-10 bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50"
                          autoComplete="new-password"
                          disabled={disabled}
                        />
                      </div>

                      <div>
                        <label className="block text-[11px] uppercase tracking-[0.24em] text-blox-muted mb-1.5">Terminal PIN</label>
                        <Input
                          type="password"
                          inputMode="numeric"
                          value={pin}
                          onChange={(e) => setPin(e.target.value)}
                          placeholder="At least 4 digits"
                          className="h-10 bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50 font-mono"
                          autoComplete="off"
                          disabled={disabled}
                        />
                      </div>

                      <div>
                        <label className="block text-[11px] uppercase tracking-[0.24em] text-blox-muted mb-1.5">Confirm PIN</label>
                        <Input
                          type="password"
                          inputMode="numeric"
                          value={confirmPin}
                          onChange={(e) => setConfirmPin(e.target.value)}
                          placeholder="Repeat PIN"
                          className="h-10 bg-blox-bg border-blox-border text-blox-text placeholder:text-blox-muted/50 font-mono"
                          autoComplete="off"
                          disabled={disabled}
                        />
                      </div>
                    </div>

                    {error && (
                      <motion.p
                        initial={{ opacity: 0, y: -4 }}
                        animate={{ opacity: 1, y: 0 }}
                        className="rounded-xl border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs leading-5 text-blox-red"
                      >
                        {error}
                      </motion.p>
                    )}

                    <Button
                      type="submit"
                      disabled={disabled}
                      className="h-10 w-full bg-blox-blue hover:bg-blox-blue/90 text-white text-sm font-medium"
                    >
                      {setupState === "checking"
                        ? "Checking setup status..."
                        : loading
                            ? "Creating admin..."
                            : "Complete First-Boot Setup"}
                    </Button>

                    <p className="text-[11px] leading-6 text-blox-muted">
                      The setup token is read from `~/.bloxos/setup-token` or provided through `BLOXOS_SETUP_TOKEN` on the server.
                    </p>
                  </form>
                )}
              </CardContent>
            </Card>
          </motion.div>
        </section>
      </div>
    </div>
  );
}
