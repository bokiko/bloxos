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
