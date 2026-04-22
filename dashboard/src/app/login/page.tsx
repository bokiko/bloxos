"use client";

import { useState, FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/contexts/AuthContext";

export default function LoginPage() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const { login } = useAuth();
  const router = useRouter();

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
    <div className="min-h-screen bg-blox-bg flex items-center justify-center">
      <div className="w-full max-w-sm">
        <div className="text-center mb-8">
          <h1 className="text-3xl font-bold tracking-tight">
            <span className="text-blox-blue">Blox</span>
            <span className="text-blox-text">OS</span>
          </h1>
          <p className="text-sm text-blox-muted mt-2">Fleet Management</p>
        </div>

        <form onSubmit={handleSubmit} className="bg-blox-card border border-blox-border rounded-lg p-6 space-y-4">
          <div>
            <label className="block text-xs text-blox-muted mb-1.5">Username</label>
            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              className="w-full px-3 py-2 text-sm rounded-md bg-blox-bg border border-blox-border text-blox-text placeholder:text-blox-muted focus:outline-none focus:border-blox-blue/50"
              placeholder="admin"
              autoFocus
              autoComplete="username"
            />
          </div>
          <div>
            <label className="block text-xs text-blox-muted mb-1.5">Password</label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full px-3 py-2 text-sm rounded-md bg-blox-bg border border-blox-border text-blox-text placeholder:text-blox-muted focus:outline-none focus:border-blox-blue/50"
              placeholder="password"
              autoComplete="current-password"
            />
          </div>

          {error && (
            <p className="text-xs text-blox-red">{error}</p>
          )}

          <button
            type="submit"
            disabled={loading || !username || !password}
            className="w-full py-2.5 rounded-md text-sm font-medium bg-blox-blue text-white hover:bg-blox-blue/90 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            {loading ? "Signing in..." : "Sign In"}
          </button>
        </form>
      </div>
    </div>
  );
}
