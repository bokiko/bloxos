"use client";

// Placeholder for the metric charts while fewer than two points exist.
//
// Monoform: the same panel and header strip the real charts use, with a flat
// shimmer where the plot will be. The old version drew a fake gradient-filled
// sparkline and a pulsing dot — a decorative chart fill and a continuous
// animation, both of which the design system rules out.

import { MF_PANEL_HEAD, MF_PANEL_TITLE } from "@/lib/monoform-classes";

interface MetricsChartsSkeletonProps {
  /** Whether to render the GPU charts (skipped on machines without GPUs). */
  hasGpu: boolean;
}

export function MetricsChartsSkeleton({ hasGpu }: MetricsChartsSkeletonProps) {
  const chartCount = hasGpu ? 4 : 2;

  return (
    <div className="space-y-4" aria-busy="true">
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {Array.from({ length: chartCount }).map((_, i) => (
          <ChartSkeleton key={i} />
        ))}
      </div>
      <p className="pt-1 text-center text-[11px] text-text-tertiary">
        Collecting data — metrics are polled every 30 seconds.
      </p>
    </div>
  );
}

function ChartSkeleton() {
  return (
    <section className="mf-panel overflow-hidden">
      <div className={MF_PANEL_HEAD}>
        <span
          className={`${MF_PANEL_TITLE} inline-block h-3 w-24 rounded bg-border-default/60 animate-shimmer`}
        />
      </div>
      <div className="px-3 py-4">
        <div className="h-[180px] w-full rounded bg-border-default/25 animate-shimmer" aria-hidden />
      </div>
    </section>
  );
}
