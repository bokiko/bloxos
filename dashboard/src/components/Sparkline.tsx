"use client";

import { useEffect, useState, useRef } from "react";
import { HUB_URL, getStoredToken } from "@/lib/session";

interface SparklineProps {
  machineId: string;
  width?: number;
  height?: number;
}

function hexToRgba(hex: string, alpha: number): string {
  const cleaned = hex.trim().replace(/^#/, "");
  if (!/^[0-9a-fA-F]{6}$/.test(cleaned)) return hex;
  const r = parseInt(cleaned.slice(0, 2), 16);
  const g = parseInt(cleaned.slice(2, 4), 16);
  const b = parseInt(cleaned.slice(4, 6), 16);
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

export function Sparkline({ machineId, width = 120, height = 28 }: SparklineProps) {
  const [points, setPoints] = useState<number[]>([]);
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const token = getStoredToken();
    const headers: Record<string, string> = {};
    if (token) headers["Authorization"] = `Bearer ${token}`;

    fetch(`${HUB_URL}/api/machines/${machineId}/metrics/history?period=30m`, { headers })
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => {
        if (data?.points) {
          setPoints(data.points.map((p: { cpu_percent: number }) => p.cpu_percent));
        }
      })
      .catch(() => {});
  }, [machineId]);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || points.length < 2) return;

    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const dpr = window.devicePixelRatio || 1;
    canvas.width = width * dpr;
    canvas.height = height * dpr;
    ctx.scale(dpr, dpr);
    ctx.clearRect(0, 0, width, height);

    // Resolve --accent from the active theme. Falls back to a hardcoded
    // brand blue if the variable can't be read (e.g. during SSR hydration).
    const computed = getComputedStyle(canvas);
    const accentRaw = computed.getPropertyValue("--accent").trim();
    const accent = accentRaw || "#3b82f6";

    const min = Math.max(0, Math.min(...points) - 5);
    const max = Math.min(100, Math.max(...points) + 5);
    const range = max - min || 1;
    const stepX = width / (points.length - 1);

    ctx.beginPath();
    ctx.strokeStyle = accent;
    ctx.lineWidth = 1.5;
    ctx.lineJoin = "round";
    ctx.lineCap = "round";

    for (let i = 0; i < points.length; i++) {
      const x = i * stepX;
      const y = height - ((points[i] - min) / range) * (height - 4) - 2;
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    ctx.stroke();

    ctx.lineTo((points.length - 1) * stepX, height);
    ctx.lineTo(0, height);
    ctx.closePath();
    const grad = ctx.createLinearGradient(0, 0, 0, height);
    grad.addColorStop(0, hexToRgba(accent, 0.18));
    grad.addColorStop(1, hexToRgba(accent, 0));
    ctx.fillStyle = grad;
    ctx.fill();
  }, [points, width, height]);

  // Empty / collecting state — a subtle dim line at the bottom edge
  // (instead of a dashed line in the middle which reads as "broken").
  if (points.length < 2) {
    return (
      <div
        style={{ width, height }}
        className="flex items-end"
        aria-label="collecting data"
      >
        <div className="w-full h-px bg-blox-border" />
      </div>
    );
  }

  return (
    <canvas
      ref={canvasRef}
      style={{ width, height }}
      className="opacity-90"
    />
  );
}
