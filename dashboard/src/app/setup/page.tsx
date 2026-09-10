"use client";

// First-boot setup. Like /login it renders outside AppShell and carries the
// Monoform canvas itself: void ground, one graphite panel for the form, a
// hairline rule between the explanation and the work. The gradient washes,
// grid wallpaper and blurred glass cards this page used to have are gone —
// the first screen an operator ever sees should look like the product.

import { FormEvent, useEffect, useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { ShieldCheck, KeyRound, LockKeyhole } from "lucide-react";
import { Input } from "@/components/ui/input";
import { BrandedHeader } from "@/components/BrandedHeader";
import { BrandingPlate } from "@/components/BrandingPlate";
import { useAuth } from "@/contexts/AuthContext";
import { HUB_URL } from "@/lib/session";
import { MF_BUTTON, MF_INPUT, MF_LABEL } from "@/lib/monoform-classes";

type SetupState = "checking" | "ready" | "error" | "complete";

const PRINCIPLES = [
  {
    icon: ShieldCheck,
    title: "Bootstrap proof",
    body: "The setup token from the server proves you control the host before the first admin exists.",
  },
  {
    icon: KeyRound,
    title: "Rotated from minute one",
    body: "The admin account and terminal PIN are stored as already-changed credentials, not shipped defaults.",
  },
  {
    icon: LockKeyhole,
    title: "One path, one time",
    body: "Once setup completes the backend closes this path and the dashboard falls back to the normal login flow.",
  },
];

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

      const loginUsername = setupData?.username || trimmedUsername;
      setCreatedUsername(loginUsername);
      setSetupState("complete");

      const loggedIn = await login(loginUsername, password);
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
    <div className="min-h-screen bg-surface-sunken">
      <div className="mx-auto grid min-h-screen w-full max-w-[1180px] lg:grid-cols-[1fr_minmax(0,520px)]">
        <section className="hidden flex-col justify-between border-r border-border-subtle px-12 py-14 lg:flex">
          <div>
            <div className="mf-kicker">First boot</div>
            {/* The page's <h1> is the wordmark in the form column, matching
                /login — this is the display headline under it. */}
            <h2 className="mt-3 max-w-xl text-[38px] font-[550] leading-[1.05] tracking-[-0.045em] text-text-primary">
              Turn a blank install into a locked-down control plane.
            </h2>
            <p className="mt-5 max-w-md text-[13px] leading-[1.7] text-text-tertiary">
              This one-time flow creates your first admin, seals the default credential path, and sets
              the terminal PIN before the dashboard ever opens.
            </p>
          </div>

          <dl className="space-y-6 border-t border-border-subtle pt-8">
            {PRINCIPLES.map(({ icon: Icon, title, body }) => (
              <div key={title} className="grid grid-cols-[20px_minmax(0,1fr)] gap-x-3.5">
                <Icon className="mt-0.5 h-4 w-4 text-accent" aria-hidden />
                <div>
                  <dt className="text-[13px] font-medium text-text-primary">{title}</dt>
                  <dd className="mt-1 max-w-md text-xs leading-[1.7] text-text-tertiary">{body}</dd>
                </div>
              </div>
            ))}
          </dl>
        </section>

        <section className="flex items-center justify-center px-4 py-12 sm:px-8">
          <div className="w-full max-w-md">
            <div className="mb-8">
              <BrandingPlate>
                <BrandedHeader size="compact" />
              </BrandingPlate>
            </div>
            <div className="mb-7">
              <div className="mf-kicker">Setup</div>
              <p className="mt-2 text-[26px] font-[560] leading-none tracking-[-0.04em] text-text-primary">
                Create the first administrator
              </p>
              <p className="mt-2.5 text-[13px] leading-[1.6] text-text-tertiary">
                Secures terminal access at the same time.
              </p>
            </div>

            <div className="mf-panel px-6 py-6">
              {setupState === "complete" ? (
                <div className="space-y-5">
                  <div className="flex items-center gap-2.5">
                    <ShieldCheck className="h-4 w-4 text-status-ok" aria-hidden />
                    <h3 className="text-[15px] font-semibold text-text-primary">Setup complete</h3>
                  </div>
                  <p className="text-[13px] leading-6 text-text-tertiary">
                    Admin <span className="font-medium text-text-primary">{createdUsername}</span> is
                    ready. {navigating ? "Opening the dashboard…" : "You can sign in now."}
                  </p>
                  {error && <FormError message={error} />}
                  {!navigating && (
                    <button
                      type="button"
                      onClick={() => router.replace("/login")}
                      className="mf-action w-full"
                    >
                      Go to login
                    </button>
                  )}
                </div>
              ) : setupState === "error" ? (
                <div className="space-y-5">
                  <div className="flex items-center gap-2.5">
                    <LockKeyhole className="h-4 w-4 text-status-critical" aria-hidden />
                    <h3 className="text-[15px] font-semibold text-text-primary">
                      Setup service unavailable
                    </h3>
                  </div>
                  <p className="text-[13px] leading-6 text-text-tertiary">
                    {error || "The dashboard could not verify whether first-boot setup is still required."}
                  </p>
                  <button
                    type="button"
                    onClick={() => {
                      setSetupState("checking");
                      setError("");
                      setSetupCheckNonce((value) => value + 1);
                    }}
                    className={`${MF_BUTTON} w-full`}
                  >
                    Retry setup check
                  </button>
                </div>
              ) : (
                <form onSubmit={handleSubmit} className="space-y-5">
                  <div className="grid gap-4 sm:grid-cols-2">
                    <div className="sm:col-span-2">
                      <label htmlFor="setup-token" className={MF_LABEL}>Setup token</label>
                      <Input
                        id="setup-token"
                        type="text"
                        value={setupToken}
                        onChange={(e) => setSetupToken(e.target.value)}
                        placeholder="Paste the one-time setup token"
                        className={`${MF_INPUT} w-full font-mono`}
                        autoComplete="off"
                        autoFocus
                        disabled={disabled}
                      />
                    </div>

                    <div className="sm:col-span-2">
                      <label htmlFor="setup-username" className={MF_LABEL}>Admin username</label>
                      <Input
                        id="setup-username"
                        type="text"
                        value={username}
                        onChange={(e) => setUsername(e.target.value)}
                        placeholder="admin"
                        className={`${MF_INPUT} w-full`}
                        autoComplete="username"
                        disabled={disabled}
                      />
                    </div>

                    <div>
                      <label htmlFor="setup-password" className={MF_LABEL}>Password</label>
                      <Input
                        id="setup-password"
                        type="password"
                        value={password}
                        onChange={(e) => setPassword(e.target.value)}
                        placeholder="Minimum 8 characters"
                        className={`${MF_INPUT} w-full`}
                        autoComplete="new-password"
                        disabled={disabled}
                      />
                    </div>

                    <div>
                      <label htmlFor="setup-confirm-password" className={MF_LABEL}>Confirm password</label>
                      <Input
                        id="setup-confirm-password"
                        type="password"
                        value={confirmPassword}
                        onChange={(e) => setConfirmPassword(e.target.value)}
                        placeholder="Repeat password"
                        className={`${MF_INPUT} w-full`}
                        autoComplete="new-password"
                        disabled={disabled}
                      />
                    </div>

                    <div>
                      <label htmlFor="setup-pin" className={MF_LABEL}>Terminal PIN</label>
                      <Input
                        id="setup-pin"
                        type="password"
                        inputMode="numeric"
                        value={pin}
                        onChange={(e) => setPin(e.target.value)}
                        placeholder="At least 4 digits"
                        className={`${MF_INPUT} w-full font-mono`}
                        autoComplete="off"
                        disabled={disabled}
                      />
                    </div>

                    <div>
                      <label htmlFor="setup-confirm-pin" className={MF_LABEL}>Confirm PIN</label>
                      <Input
                        id="setup-confirm-pin"
                        type="password"
                        inputMode="numeric"
                        value={confirmPin}
                        onChange={(e) => setConfirmPin(e.target.value)}
                        placeholder="Repeat PIN"
                        className={`${MF_INPUT} w-full font-mono`}
                        autoComplete="off"
                        disabled={disabled}
                      />
                    </div>
                  </div>

                  {error && <FormError message={error} />}

                  <button
                    type="submit"
                    disabled={disabled}
                    className="mf-action inline-flex w-full items-center justify-center gap-2 disabled:opacity-50"
                  >
                    {setupState === "checking" ? (
                      <>
                        <Spinner />
                        Checking setup status…
                      </>
                    ) : loading ? (
                      <>
                        <Spinner />
                        Creating admin…
                      </>
                    ) : (
                      "Complete first-boot setup"
                    )}
                  </button>

                  <p className="text-[11px] leading-[1.7] text-text-tertiary">
                    The setup token is read from{" "}
                    <code className="font-mono text-text-secondary">~/.bloxos/setup-token</code> or
                    provided through{" "}
                    <code className="font-mono text-text-secondary">BLOXOS_SETUP_TOKEN</code> on the
                    server.
                  </p>
                </form>
              )}
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}

function FormError({ message }: { message: string }) {
  return (
    <p
      role="alert"
      className="rounded-lg border border-status-critical/40 bg-status-critical-tint px-3 py-2 text-xs leading-5 text-status-critical"
    >
      {message}
    </p>
  );
}

function Spinner() {
  return (
    <span
      className="h-3 w-3 rounded-full border-2 border-white/40 border-t-white animate-spin"
      aria-hidden
    />
  );
}
