"use client";

interface RankedBarItem {
  id: string;
  label: string;
  value: number;
  unit?: string;
}

interface RankedBarProps {
  items: RankedBarItem[];
  maxItems?: number;
  title?: string;
}

export function RankedBar({ items, maxItems = 5, title }: RankedBarProps) {
  const sorted = [...items]
    .sort((a, b) => b.value - a.value)
    .slice(0, maxItems);

  const maxValue = sorted.length > 0 ? Math.max(...sorted.map((i) => i.value)) : 100;

  if (sorted.length === 0) {
    return (
      <div className="bg-surface-raised border border-border-subtle rounded-xl p-4">
        {title && (
          <h4 className="text-xs font-medium text-text-secondary uppercase tracking-wider mb-3">
            {title}
          </h4>
        )}
        <div className="text-xs text-text-tertiary py-4 text-center">
          No data available
        </div>
      </div>
    );
  }

  return (
    <div className="bg-surface-raised border border-border-subtle rounded-xl p-4">
      {title && (
        <h4 className="text-xs font-medium text-text-secondary uppercase tracking-wider mb-3">
          {title}
        </h4>
      )}
      <div className="space-y-2.5">
        {sorted.map((item, index) => (
          <div key={item.id} className="flex items-center gap-3">
            <div className="text-text-tertiary font-mono text-xs w-4 text-right">
              {index + 1}
            </div>
            <div className="flex-1 min-w-0">
              <div className="flex items-baseline justify-between gap-2 mb-1">
                <span className="text-xs text-text-primary truncate font-medium">
                  {item.label}
                </span>
                <span className="text-xs font-mono tabular-nums text-text-secondary shrink-0">
                  {item.value.toFixed(1)}
                  {item.unit || "%"}
                </span>
              </div>
              <div className="h-1.5 rounded-full bg-surface-sunken overflow-hidden">
                <div
                  className="h-full rounded-full transition-all duration-500 ease-out"
                  style={{
                    width: `${(item.value / maxValue) * 100}%`,
                    background:
                      item.value >= 90
                        ? "var(--status-critical)"
                        : item.value >= 75
                        ? "var(--status-warning)"
                        : "var(--accent)",
                  }}
                />
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
