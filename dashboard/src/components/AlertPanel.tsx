"use client";

import { AlertData } from "@/lib/demo-data";
import { X, AlertTriangle, CheckCircle } from "lucide-react";

interface AlertPanelProps {
  open: boolean;
  onClose: () => void;
  alerts: AlertData[];
  onAcknowledge: (id: string) => void;
  onAcknowledgeAll: () => void;
}

function timeAgo(dateStr?: string): string {
  if (!dateStr) return "";
  const sec = Math.floor((Date.now() - new Date(dateStr).getTime()) / 1000);
  if (sec < 0 || isNaN(sec)) return "just now";
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const d = Math.floor(hr / 24);
  return `${d}d ago`;
}

export function AlertPanel({ open, onClose, alerts, onAcknowledge, onAcknowledgeAll }: AlertPanelProps) {
  if (!open) return null;

  return (
    <>
      {/* Backdrop */}
      <div className="fixed inset-0 z-[60] bg-black/40" onClick={onClose} />

      {/* Panel */}
      <div className="fixed right-0 top-0 bottom-0 z-[70] w-full max-w-md bg-blox-bg border-l border-blox-border shadow-2xl flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-blox-border">
          <div className="flex items-center gap-2">
            <AlertTriangle className="w-4 h-4 text-blox-amber" />
            <h2 className="text-sm font-semibold text-blox-text">Active Alerts</h2>
            <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-blox-red/10 text-blox-red border border-blox-red/20">
              {alerts.length}
            </span>
          </div>
          <div className="flex items-center gap-2">
            {alerts.length > 0 && (
              <button
                onClick={onAcknowledgeAll}
                className="text-[10px] px-2 py-1 rounded bg-blox-blue/10 text-blox-blue border border-blox-blue/20 hover:bg-blox-blue/20 transition-colors"
              >
                Acknowledge All
              </button>
            )}
            <button
              onClick={onClose}
              className="p-1.5 rounded hover:bg-blox-border/50 text-blox-muted hover:text-blox-text transition-colors"
            >
              <X className="w-4 h-4" />
            </button>
          </div>
        </div>

        {/* Alert list */}
        <div className="flex-1 overflow-y-auto">
          {alerts.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-blox-muted">
              <CheckCircle className="w-10 h-10 mb-3 opacity-20" />
              <p className="text-sm">No active alerts</p>
              <p className="text-xs mt-1">All systems operating normally</p>
            </div>
          ) : (
            <div className="divide-y divide-blox-border/50">
              {alerts.map((alert) => (
                <div key={alert.id} className="px-5 py-4 hover:bg-blox-card/50 transition-colors">
                  <div className="flex items-start justify-between gap-3">
                    <div className="flex items-start gap-3 min-w-0">
                      <div className="mt-0.5">
                        {alert.severity === "critical" ? (
                          <span className="text-blox-red text-sm">&#x1F534;</span>
                        ) : (
                          <span className="text-blox-amber text-sm">&#x26A0;&#xFE0F;</span>
                        )}
                      </div>
                      <div className="min-w-0">
                        <div className="flex items-center gap-2 mb-1">
                          <span className="text-xs font-medium text-blox-text truncate">
                            {alert.hostname || alert.machine_id}
                          </span>
                          <span className={`text-[9px] px-1.5 py-0.5 rounded-full border ${
                            alert.severity === "critical"
                              ? "bg-blox-red/10 text-blox-red border-blox-red/20"
                              : "bg-blox-amber/10 text-blox-amber border-blox-amber/20"
                          }`}>
                            {alert.severity}
                          </span>
                        </div>
                        <p className="text-xs text-blox-muted leading-relaxed">{alert.message}</p>
                        <p className="text-[10px] text-blox-muted mt-1">{timeAgo(alert.triggered_at)}</p>
                      </div>
                    </div>
                    <button
                      onClick={() => onAcknowledge(alert.id)}
                      className="text-[10px] px-2 py-1 rounded bg-blox-border text-blox-muted hover:text-blox-text hover:bg-blox-border/80 transition-colors shrink-0"
                    >
                      Ack
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </>
  );
}
