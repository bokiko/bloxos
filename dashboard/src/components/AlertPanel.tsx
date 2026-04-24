"use client";

import { AlertData } from "@/lib/demo-data";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { ScrollArea } from "@/components/ui/scroll-area";
import { AlertTriangle, CheckCircle, AlertOctagon } from "lucide-react";

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
  return (
    <Sheet open={open} onOpenChange={(o) => { if (!o) onClose(); }}>
      <SheetContent
        side="right"
        className="bg-blox-bg border-l-blox-border w-full sm:max-w-md p-0 gap-0"
      >
        <SheetHeader className="px-5 pt-5 pb-4 border-b border-blox-border">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2.5">
              <AlertTriangle className="w-4 h-4 text-amber-400" />
              <SheetTitle className="text-sm font-semibold text-blox-text">Active Alerts</SheetTitle>
              <Badge variant="outline" className="border-red-500/30 bg-red-500/10 text-red-400 text-[10px] px-1.5 h-auto py-0">
                {alerts.length}
              </Badge>
            </div>
            {alerts.length > 0 && (
              <Button
                variant="ghost"
                size="xs"
                onClick={onAcknowledgeAll}
                className="text-[10px] text-blox-blue hover:bg-blox-blue/10"
              >
                Acknowledge All
              </Button>
            )}
          </div>
        </SheetHeader>

        <ScrollArea className="flex-1 h-[calc(100vh-80px)]">
          {alerts.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-20 text-blox-muted">
              <CheckCircle className="w-10 h-10 mb-3 opacity-20" />
              <p className="text-sm">No active alerts</p>
              <p className="text-xs mt-1 text-blox-muted/60">All systems operating normally</p>
            </div>
          ) : (
            <div>
              {alerts.map((alert, i) => (
                <div key={alert.id}>
                  <div className="px-5 py-4 hover:bg-blox-card/50 transition-colors">
                    <div className="flex items-start justify-between gap-3">
                      <div className="flex items-start gap-3 min-w-0">
                        <div className="mt-0.5 shrink-0">
                          {alert.severity === "critical" ? (
                            <AlertOctagon className="w-4 h-4 text-red-400" />
                          ) : (
                            <AlertTriangle className="w-4 h-4 text-amber-400" />
                          )}
                        </div>
                        <div className="min-w-0">
                          <div className="flex items-center gap-2 mb-1">
                            <span className="text-xs font-medium text-blox-text truncate">
                              {alert.hostname || alert.machine_id}
                            </span>
                            <Badge
                              variant="outline"
                              className={`text-[10px] px-1.5 h-auto py-0 ${
                                alert.severity === "critical"
                                  ? "border-red-500/30 bg-red-500/10 text-red-400"
                                  : "border-amber-500/30 bg-amber-500/10 text-amber-400"
                              }`}
                            >
                              {alert.severity}
                            </Badge>
                          </div>
                          <p className="text-xs text-blox-muted leading-relaxed">{alert.message}</p>
                          <p className="text-[10px] text-blox-muted/60 mt-1 font-mono tabular-nums">{timeAgo(alert.triggered_at)}</p>
                        </div>
                      </div>
                      <Button
                        variant="outline"
                        size="xs"
                        onClick={() => onAcknowledge(alert.id)}
                        className="text-[10px] text-blox-muted border-blox-border hover:text-blox-text shrink-0"
                      >
                        Ack
                      </Button>
                    </div>
                  </div>
                  {i < alerts.length - 1 && <Separator className="bg-blox-border/50" />}
                </div>
              ))}
            </div>
          )}
        </ScrollArea>
      </SheetContent>
    </Sheet>
  );
}
