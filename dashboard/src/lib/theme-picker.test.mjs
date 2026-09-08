import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

test("quick theme picker uses the complete shared theme registry", () => {
  const registry = readFileSync(new URL("../contexts/ThemeContext.tsx", import.meta.url), "utf8");
  const menu = readFileSync(new URL("../components/UserMenu.tsx", import.meta.url), "utf8");
  const names = registry.match(/export const THEME_NAMES[^=]*=\s*\[([\s\S]*?)\];/);
  assert.ok(names, "theme names must be exported for consumers");
  for (const name of ["bloxos", "solarized", "dracula", "nord", "tokyo-night", "mission-control", "graphite", "verdant"]) {
    assert.ok(names[1].includes(`"${name}"`), `missing ${name}`);
  }
  assert.match(menu, /THEME_NAMES\.map\(/);
  assert.doesNotMatch(menu, /const THEME_ORDER/);
  assert.match(menu, /aria-pressed=\{active\}/);
});

test("fleet header gives branding and actions separate rows on narrow screens", () => {
  const page = readFileSync(new URL("../app/page.tsx", import.meta.url), "utf8");
  assert.match(page, /flex-wrap sm:flex-nowrap/);
  assert.match(page, /min-h-14 py-2 sm:py-0 sm:h-14/);
  assert.match(page, /flex w-full sm:w-auto items-center justify-end gap-1/);
});
