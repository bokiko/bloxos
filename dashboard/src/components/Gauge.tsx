"use client";

import { RadialBarChart, RadialBar, ResponsiveContainer, PolarAngleAxis } from "recharts";
import { gaugeReading } from "@/lib/gauge-data.mjs";

export type GaugeVariant = "neutral" | "ok" | "warning" | "critical";

interface GaugeProps {
  /** 0–100 percentage value */
  value: number;
  /** Display label */
  label: string;
  /** Optional unit suffix (e.g., "%", "°C") */
  unit?: string;
  /** Optional explicit variant, otherwise derived from value and thresholds */
  variant?: GaugeVariant;
  /** Thresholds for color: [warning, critical]. Default: [70, 90] */
  thresholds?: [number, number];
  /** Size variant */
  size?: "sm" | "md" | "lg";
  /** Display mode */
  mode?: "radial" | "half";
}

function deriveVariant(value: number, thresholds: [number, number]): GaugeVariant {
  if (value <= 0) return "neutral";
  if (value >= thresholds[1]) return "critical";
  if (value >= thresholds[0]) return "warning";
  return "ok";
}

function getColor(variant: GaugeVariant): string {
  switch (variant) {
    case "critical":
      return "var(--status-critical)";
    case "warning":
      return "var(--status-warning)";
    case "ok":
      return "var(--accent)";
    case "neutral":
    default:
      return "var(--border-default)";
  }
}

export function Gauge({
  value,
  label,
  unit = "%",
  variant,
  thresholds = [70, 90],
  size = "md",
  mode = "radial",
}: GaugeProps) {
  const reading = gaugeReading(value);
  const v = variant ?? (Number.isFinite(value) ? deriveVariant(value, thresholds) : "neutral");
  const color = getColor(v);

  const dimensions = {
    sm: { width: 100, height: 100, fontSize: "1.25rem", labelSize: "0.625rem" },
    md: { width: 140, height: 140, fontSize: "1.75rem", labelSize: "0.75rem" },
    lg: { width: 180, height: 180, fontSize: "2.25rem", labelSize: "0.875rem" },
  }[size];

  const data = [{ value: reading.arc, fill: color }];

  const chartConfig = mode === "half" 
    ? {
        startAngle: 180,
        endAngle: 0,
        innerRadius: "75%",
        outerRadius: "100%",
      }
    : {
        startAngle: 90,
        endAngle: -270,
        innerRadius: "70%",
        outerRadius: "100%",
      };

  return (
    <div className="relative flex flex-col items-center">
      <div
        className="relative"
        style={{ width: dimensions.width, height: mode === "half" ? dimensions.height / 2 : dimensions.height }}
      >
        <ResponsiveContainer width="100%" height="100%">
          <RadialBarChart
            data={data}
            {...chartConfig}
            cx="50%"
            cy={mode === "half" ? "100%" : "50%"}
            barSize={size === "sm" ? 8 : size === "lg" ? 14 : 10}
          >
            <PolarAngleAxis type="number" domain={[0, 100]} tick={false} />
            <RadialBar
              dataKey="value"
              cornerRadius={4}
              background={{ fill: "var(--surface-sunken)" }}
            />
          </RadialBarChart>
        </ResponsiveContainer>

        <div
          className="absolute inset-0 flex flex-col items-center justify-center"
          style={{ top: mode === "half" ? "20%" : "0" }}
        >
          <div
            className="font-mono tabular-nums font-semibold text-text-primary"
            style={{ fontSize: dimensions.fontSize }}
          >
            {reading.label}
            <span className="text-text-tertiary" style={{ fontSize: `${parseFloat(dimensions.fontSize) * 0.6}rem` }}>
              {unit}
            </span>
          </div>
        </div>
      </div>

      <div
        className="mt-1 text-text-secondary font-medium uppercase tracking-wider text-center"
        style={{ fontSize: dimensions.labelSize }}
      >
        {label}
      </div>
    </div>
  );
}

interface GaugeClusterProps {
  gauges: Array<{
    value: number;
    label: string;
    unit?: string;
    variant?: GaugeVariant;
    thresholds?: [number, number];
  }>;
  size?: "sm" | "md" | "lg";
}

export function GaugeCluster({ gauges, size = "md" }: GaugeClusterProps) {
  return (
    <div className="flex flex-wrap items-center justify-center gap-6">
      {gauges.map((gauge, i) => (
        <Gauge key={i} {...gauge} size={size} />
      ))}
    </div>
  );
}
