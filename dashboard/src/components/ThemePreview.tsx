import { THEMES, type ThemeName } from "@/contexts/ThemeContext";

/** Small composition preview, using each design's own surfaces and geometry. */
export function ThemePreview({ name }: { name: ThemeName }) {
  const { preview: p } = THEMES[name];
  const radius = name === "mission-control" ? 2 : name === "verdant" ? 12 : 6;
  return (
    <div aria-hidden="true" className="h-28 mb-3 overflow-hidden p-2.5" style={{ background: p.background, borderRadius: radius }}>
      <div className="flex items-center gap-1 mb-2">
        <span className="h-2 w-2 rounded-full" style={{ background: p.accent }} />
        <span className="h-1 w-12 rounded" style={{ background: p.text, opacity: .6 }} />
      </div>
      <div className="grid grid-cols-3 gap-1.5">
        {[0, 1, 2].map(i => <div key={i} className="h-7 p-1.5" style={{ background: p.surface, borderRadius: radius }}><div className="h-2 w-5" style={{ background: i === 0 ? p.accent : p.text, opacity: .8 }} /></div>)}
      </div>
      <div className="mt-1.5 h-10 flex items-end gap-2 px-3 pb-1.5" style={{ background: p.surface, borderRadius: radius, boxShadow: name === "graphite" ? "inset 0 1px #ffffff18, 0 3px 6px #0006" : undefined }}>
        {[35, 60, 45, 85, 65, 95, 72, 80].map((h, i) => <span key={i} className="flex-1" style={{ height: `${h}%`, background: p.accent, opacity: .5 + i / 16, borderRadius: radius / 2 }} />)}
      </div>
    </div>
  );
}
