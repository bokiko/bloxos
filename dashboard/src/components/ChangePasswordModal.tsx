"use client";

import { useState, FormEvent } from "react";
import { useAuth } from "@/contexts/AuthContext";

const HUB_URL = process.env.NEXT_PUBLIC_HUB_URL || "http://localhost:4000";

interface ChangePasswordModalProps {
  type: "password" | "pin";
  onComplete: () => void;
}

export function ChangePasswordModal({ type, onComplete }: ChangePasswordModalProps) {
  const { authFetch } = useAuth();
  const [current, setCurrent] = useState("");
  const [newValue, setNewValue] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const isPassword = type === "password";
  const title = isPassword ? "Change Default Password" : "Change Default PIN";
  const subtitle = isPassword
    ? "You are using the default password. Please change it before continuing."
    : "You are using the default terminal PIN. Please change it before continuing.";
  const currentLabel = isPassword ? "Current Password" : "Current PIN";
  const currentPlaceholder = isPassword ? "bloxos" : "1234";
  const newLabel = isPassword ? "New Password" : "New PIN";
  const confirmLabel = isPassword ? "Confirm New Password" : "Confirm New PIN";
  const minLength = isPassword ? 6 : 4;
  const endpoint = isPassword ? "/api/auth/change-password" : "/api/auth/change-pin";
  const bodyKey = isPassword
    ? { current: "current_password", new: "new_password" }
    : { current: "current_pin", new: "new_pin" };

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");

    if (newValue.length < minLength) {
      setError(`Must be at least ${minLength} characters`);
      return;
    }
    if (newValue !== confirm) {
      setError(isPassword ? "Passwords do not match" : "PINs do not match");
      return;
    }
    if (newValue === current) {
      setError(`New ${type} must be different from current`);
      return;
    }

    setLoading(true);
    try {
      const res = await authFetch(`${HUB_URL}${endpoint}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          [bodyKey.current]: current,
          [bodyKey.new]: newValue,
        }),
      });
      if (!res.ok) {
        const data = await res.json();
        setError(data.error || "Failed to update");
        setLoading(false);
        return;
      }
      onComplete();
    } catch {
      setError("Network error");
    }
    setLoading(false);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm">
      <div className="w-full max-w-md mx-4">
        <div className="bg-blox-card border border-blox-border rounded-lg p-6">
          <div className="text-center mb-6">
            <div className="w-12 h-12 mx-auto mb-3 rounded-full bg-amber-500/10 flex items-center justify-center">
              <svg className="w-6 h-6 text-amber-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z" />
              </svg>
            </div>
            <h2 className="text-lg font-semibold text-blox-text">{title}</h2>
            <p className="text-sm text-blox-muted mt-1">{subtitle}</p>
          </div>

          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label className="block text-xs text-blox-muted mb-1.5">{currentLabel}</label>
              <input
                type={isPassword ? "password" : "text"}
                value={current}
                onChange={(e) => setCurrent(e.target.value)}
                className="w-full px-3 py-2 text-sm rounded-md bg-blox-bg border border-blox-border text-blox-text placeholder:text-blox-muted focus:outline-none focus:border-blox-blue/50"
                placeholder={currentPlaceholder}
                autoFocus
              />
            </div>
            <div>
              <label className="block text-xs text-blox-muted mb-1.5">{newLabel}</label>
              <input
                type={isPassword ? "password" : "text"}
                value={newValue}
                onChange={(e) => setNewValue(e.target.value)}
                className="w-full px-3 py-2 text-sm rounded-md bg-blox-bg border border-blox-border text-blox-text placeholder:text-blox-muted focus:outline-none focus:border-blox-blue/50"
                placeholder={`Min ${minLength} characters`}
              />
            </div>
            <div>
              <label className="block text-xs text-blox-muted mb-1.5">{confirmLabel}</label>
              <input
                type={isPassword ? "password" : "text"}
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                className="w-full px-3 py-2 text-sm rounded-md bg-blox-bg border border-blox-border text-blox-text placeholder:text-blox-muted focus:outline-none focus:border-blox-blue/50"
                placeholder="Confirm"
              />
            </div>

            {error && <p className="text-xs text-blox-red">{error}</p>}

            <button
              type="submit"
              disabled={loading || !current || !newValue || !confirm}
              className="w-full py-2.5 rounded-md text-sm font-medium bg-blox-blue text-white hover:bg-blox-blue/90 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              {loading ? "Updating..." : `Change ${isPassword ? "Password" : "PIN"}`}
            </button>
          </form>
        </div>
      </div>
    </div>
  );
}
