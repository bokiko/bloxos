export function SkeletonText({ width = 'w-32', className = '' }: { width?: string; className?: string }) {
  return <div className={`h-4 bg-slate-700/50 rounded ${width} animate-pulse ${className}`} />;
}

export function SkeletonBox({
  width = 'w-full',
  height = 'h-24',
  className = '',
}: {
  width?: string;
  height?: string;
  className?: string;
}) {
  return <div className={`bg-slate-700/50 rounded-xl ${width} ${height} animate-pulse ${className}`} />;
}

export function SkeletonCard({ className = '' }: { className?: string }) {
  return (
    <div className={`bg-slate-800/50 rounded-xl border border-slate-700/50 p-5 animate-pulse ${className}`}>
      <div className="flex items-start justify-between mb-4">
        <div className="space-y-2">
          <div className="h-5 w-40 bg-slate-700/50 rounded" />
          <div className="h-3 w-24 bg-slate-700/50 rounded" />
        </div>
        <div className="h-8 w-20 bg-slate-700/50 rounded-lg" />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div className="bg-slate-900/50 rounded-lg p-3 h-16" />
        <div className="bg-slate-900/50 rounded-lg p-3 h-16" />
      </div>
    </div>
  );
}

export function SkeletonTable({ rows = 5, cols = 4 }: { rows?: number; cols?: number }) {
  return (
    <div className="bg-slate-800/50 rounded-xl border border-slate-700/50 overflow-hidden animate-pulse">
      <div className="grid gap-px bg-slate-700/30">
        {Array.from({ length: rows }).map((_, i) => (
          <div key={i} className="bg-slate-800/80 px-5 py-4 flex items-center gap-4">
            {Array.from({ length: cols }).map((_, j) => (
              <div
                key={j}
                className={`h-4 bg-slate-700/50 rounded ${j === 0 ? 'w-40' : 'w-24'}`}
              />
            ))}
          </div>
        ))}
      </div>
    </div>
  );
}

// Page-specific skeletons

export function SkeletonRigCard() {
  return (
    <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-5 animate-pulse">
      <div className="flex items-start justify-between mb-4">
        <div className="flex items-center gap-3">
          <div className="w-4 h-4 bg-slate-700/50 rounded" />
          <div className="w-3 h-3 bg-slate-700/50 rounded-full" />
          <div className="space-y-2">
            <div className="h-5 w-36 bg-slate-700/50 rounded" />
            <div className="h-3 w-48 bg-slate-700/50 rounded" />
          </div>
        </div>
        <div className="h-6 w-16 bg-slate-700/50 rounded-lg" />
      </div>
      <div className="grid grid-cols-2 md:grid-cols-5 gap-4 mb-4">
        {Array.from({ length: 5 }).map((_, i) => (
          <div key={i} className="bg-slate-900/50 rounded-lg p-3">
            <div className="h-3 w-12 bg-slate-700/50 rounded mb-2" />
            <div className="h-5 w-16 bg-slate-700/50 rounded" />
          </div>
        ))}
      </div>
      <div className="flex gap-2 pt-3 border-t border-slate-700/50">
        {Array.from({ length: 3 }).map((_, i) => (
          <div key={i} className="h-7 w-28 bg-slate-700/50 rounded-lg" />
        ))}
      </div>
    </div>
  );
}

export function SkeletonStatCard() {
  return (
    <div className="bg-slate-800 rounded-xl p-6 border border-slate-700 animate-pulse">
      <div className="flex items-center justify-between">
        <div className="space-y-2">
          <div className="h-3 w-20 bg-slate-700/50 rounded" />
          <div className="h-7 w-28 bg-slate-700/50 rounded" />
        </div>
        <div className="w-12 h-12 bg-slate-700/50 rounded-lg" />
      </div>
    </div>
  );
}

export function SkeletonAlertItem() {
  return (
    <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-4 animate-pulse">
      <div className="flex items-start gap-4">
        <div className="w-10 h-10 bg-slate-700/50 rounded-lg shrink-0" />
        <div className="flex-1 space-y-2">
          <div className="h-4 w-48 bg-slate-700/50 rounded" />
          <div className="h-3 w-full bg-slate-700/50 rounded" />
          <div className="flex gap-3">
            <div className="h-3 w-20 bg-slate-700/50 rounded" />
            <div className="h-3 w-16 bg-slate-700/50 rounded" />
          </div>
        </div>
        <div className="h-6 w-16 bg-slate-700/50 rounded-lg" />
      </div>
    </div>
  );
}

export function SkeletonProfileCard() {
  return (
    <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-5 animate-pulse">
      <div className="flex items-start justify-between mb-4">
        <div className="space-y-2">
          <div className="h-5 w-32 bg-slate-700/50 rounded" />
          <div className="h-4 w-16 bg-slate-700/50 rounded" />
        </div>
        <div className="flex gap-1">
          <div className="w-8 h-8 bg-slate-700/50 rounded-lg" />
          <div className="w-8 h-8 bg-slate-700/50 rounded-lg" />
        </div>
      </div>
      <div className="grid grid-cols-2 gap-3">
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="bg-slate-900/50 rounded-lg p-2">
            <div className="h-3 w-16 bg-slate-700/50 rounded mb-1" />
            <div className="h-4 w-12 bg-slate-700/50 rounded" />
          </div>
        ))}
      </div>
    </div>
  );
}

export function SkeletonPoolRow() {
  return (
    <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-5 animate-pulse">
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-4">
          <div className="w-12 h-12 bg-slate-700/50 rounded-xl" />
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <div className="h-5 w-32 bg-slate-700/50 rounded" />
              <div className="h-4 w-12 bg-slate-700/50 rounded" />
            </div>
            <div className="h-3 w-64 bg-slate-700/50 rounded" />
          </div>
        </div>
        <div className="flex gap-2">
          <div className="w-8 h-8 bg-slate-700/50 rounded-lg" />
          <div className="w-8 h-8 bg-slate-700/50 rounded-lg" />
        </div>
      </div>
    </div>
  );
}

export function SkeletonWalletRow() {
  return (
    <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-5 animate-pulse">
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-4">
          <div className="w-12 h-12 bg-slate-700/50 rounded-xl" />
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <div className="h-5 w-28 bg-slate-700/50 rounded" />
              <div className="h-4 w-12 bg-slate-700/50 rounded" />
            </div>
            <div className="h-3 w-72 bg-slate-700/50 rounded" />
          </div>
        </div>
        <div className="flex gap-2">
          <div className="w-8 h-8 bg-slate-700/50 rounded-lg" />
          <div className="w-8 h-8 bg-slate-700/50 rounded-lg" />
        </div>
      </div>
    </div>
  );
}
