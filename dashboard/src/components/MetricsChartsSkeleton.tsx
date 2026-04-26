"use client";

import { useId } from "react";

interface MetricsChartsSkeletonProps {
  /** Whether to render the GPU charts (skipped on machines without GPUs). */
  hasGpu: boolean;
}

export function MetricsChartsSkeleton({ hasGpu }: MetricsChartsSkeletonProps) {
  const chartCount = hasGpu ? 4 : 2;

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        {Array.from({ length: chartCount }).map((_, i) => (
          <ChartSkeleton key={i} />
        ))}
      </div>
      <div className="flex items-center justify-center gap-2 text-[11px] text-blox-muted/70 pt-2">
        <span className="relative flex h-1.5 w-1.5">
          <span className="animate-status-pulse absolute inline-flex h-full w-full rounded-full bg-blox-blue/60 opacity-75" />
          <span className="relative inline-flex rounded-full h-1.5 w-1.5 bg-blox-blue/80" />
        </span>
        <span>Collecting data — metrics are polled every 30 seconds</span>
      </div>
    </div>
  );
}

function ChartSkeleton() {
  // Each skeleton instance needs a unique id so the <defs><linearGradient>
  // doesn't collide when multiple skeletons render together.
  const gradId = useId();
  const points = [22, 38, 31, 45, 39, 52, 48, 60, 55, 68, 62, 70, 66];
  const max = Math.max(...points);
  const w = 100;
  const h = 60;
  const stepX = w / (points.length - 1);
  const path = points
    .map((p, i) => {
      const x = i * stepX;
      const y = h - (p / max) * h * 0.9 - 4;
      return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
  const fill = `${path} L${w},${h} L0,${h} Z`;

  return (
    <div className="bg-blox-bg/50 border border-blox-border/50 rounded-xl p-4">
      <div className="h-3 w-24 bg-blox-border/60 rounded mb-3 animate-shimmer" />

      <div
        className="relative w-full overflow-hidden"
        style={{ height: 160 }}
        aria-hidden
      >
        <svg
          viewBox="0 0 100 60"
          preserveAspectRatio="none"
          className="absolute inset-0 w-full h-full opacity-30"
        >
          <defs>
            <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--accent)" stopOpacity="0.3" />
              <stop offset="100%" stopColor="var(--accent)" stopOpacity="0" />
            </linearGradient>
          </defs>
          <path d={fill} fill={`url(#${gradId})`} />
          <path
            d={path}
            fill="none"
            stroke="var(--accent)"
            strokeOpacity="0.4"
            strokeWidth="1.2"
            vectorEffect="non-scaling-stroke"
            strokeLinejoin="round"
          />
        </svg>

        <div className="absolute inset-0 animate-shimmer rounded" style={{ opacity: 0.3 }} />
      </div>
    </div>
  );
}
