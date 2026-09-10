import test from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join, relative } from "node:path";

/* ============================================================================
 * Monoform guards.
 *
 * BloxOS used to ship five selectable dashboard layouts and an eight-palette
 * theme gallery. That is a product decision that was reversed, not a bug that
 * was fixed: there is now ONE visual system with two contrast modes. These
 * tests exist so the old system cannot creep back in a later feature branch —
 * they fail on the *presence* of the retired vocabulary, not on its behaviour.
 * ========================================================================== */

const SRC = fileURLToPath(new URL("..", import.meta.url));

/**
 * Every active dashboard source file. Test files are excluded on purpose: a
 * test that asserts the absence of `useDesign` has to write `useDesign` down,
 * so scanning tests would make these guards unwritable. Everything the browser
 * actually loads is in scope.
 */
function activeSources() {
  const extensions = [".ts", ".tsx", ".mts", ".mjs", ".css"];
  const files = [];
  const walk = (dir) => {
    for (const entry of readdirSync(dir)) {
      const path = join(dir, entry);
      if (statSync(path).isDirectory()) {
        walk(path);
        continue;
      }
      if (entry.endsWith(".test.mjs")) continue;
      if (extensions.some((ext) => entry.endsWith(ext))) files.push(path);
    }
  };
  walk(SRC);
  return files.map((path) => [relative(SRC, path), readFileSync(path, "utf8")]);
}

/**
 * The retired vocabulary. Theme names are matched only in the forms they had
 * as identifiers — quoted values and `theme-*` classes — because words like
 * "graphite" are also ordinary English used to describe a Monoform surface.
 */
const FORBIDDEN = [
  ["useDesign", /\buseDesign\b/],
  ["DesignProvider", /\bDesignProvider\b/],
  ["DesignContext", /\bDesignContext\b/],
  ["design-prefs", /design-prefs/],
  ["data-layout", /data-layout|dataset\.layout\s*=/],
  ["data-design-color", /data-design-color|dataset\.designColor\s*=/],
  ["FleetWall", /\bFleetWall\b/],
  ["FleetGrove", /\bFleetGrove\b/],
  ["FleetConsole", /\bFleetConsole\b/],
  ["FleetLedger", /\bFleetLedger\b/],
  [
    "a retired theme name",
    /(["'`])(?:solarized|dracula|nord|tokyo-night|mission-control|graphite|verdant)\1|theme-(?:solarized|dracula|nord|tokyo-night|mission-control|graphite|verdant)\b/,
  ],
  ["a retired layout stylesheet", /design-(?:layouts|themes|pages)\.css/],
  ["the retired design cache key", /bloxos-design/],
  ["the retired theme-name key", /bloxos-theme-name/],
];

/**
 * The single deliberate exemption. ThemeContext and the layout.tsx bootstrap
 * both read `bloxos-theme-mode` once so an existing dark choice survives the
 * reset, and both delete the retired dataset attributes off <html>. That is
 * migration code for the system being removed, so it names it.
 */
const MIGRATION_EXEMPT = new Set([
  "contexts/ThemeContext.tsx",
  "app/layout.tsx",
]);

test("no legacy design system remains in active dashboard source", () => {
  const offences = [];
  for (const [file, source] of activeSources()) {
    for (const [name, pattern] of FORBIDDEN) {
      if (!pattern.test(source)) continue;
      // The exempt files may clear the retired attributes; they may not read
      // a design preference or name a retired palette.
      if (MIGRATION_EXEMPT.has(file) && (name === "data-layout" || name === "data-design-color")) {
        continue;
      }
      offences.push(`${file}: ${name}`);
    }
  }
  assert.deepEqual(offences, [], `retired design system found in active source:\n${offences.join("\n")}`);
});

test("the deleted layout and theme modules are gone, not merely unimported", () => {
  const present = new Set(activeSources().map(([file]) => file));
  for (const gone of [
    "app/design-layouts.css",
    "app/design-pages.css",
    "app/design-themes.css",
    "contexts/DesignContext.tsx",
    "components/DesignSettings.tsx",
    "components/ThemePreview.tsx",
    "components/FleetPulse.tsx",
    "components/settings/ThemeSettings.tsx",
    "components/fleet/FleetWall.tsx",
    "components/fleet/FleetGrove.tsx",
    "components/fleet/FleetConsole.tsx",
    "components/fleet/FleetLedger.tsx",
    "lib/design-prefs.mjs",
  ]) {
    assert.ok(!present.has(gone), `${gone} must not come back`);
  }
  // ...but the aggregation the Overview depends on is not part of the deletion.
  assert.ok(present.has("components/fleet/fleetModel.ts"), "fleetModel.ts is live Overview logic");
  assert.ok(present.has("components/fleet/useFleetData.ts"), "useFleetData.ts is live Overview logic");
});

test("appearance is exactly dark and light, with no retired theme registry", () => {
  const context = readFileSync(new URL("../contexts/ThemeContext.tsx", import.meta.url), "utf8");
  const css = readFileSync(new URL("../app/monoform.css", import.meta.url), "utf8");

  const union = context.match(/export type AppearanceMode\s*=\s*([^;]+);/);
  assert.ok(union, "AppearanceMode must be exported");
  const modes = [...union[1].matchAll(/"([^"]+)"/g)].map((m) => m[1]).sort();
  assert.deepEqual(modes, ["dark", "light"], "the only themes are dark and light");

  // No theme gallery, no system mode, no per-theme registry survives. The
  // gallery is what Monoform removed; adding a second theme does not bring it
  // back, and "system" stays gone — the theme is an explicit user choice.
  assert.doesNotMatch(context, /\bTHEME_NAMES\b|\bTHEMES\b|\bThemeName\b|\bResolvedMode\b|DARK_ONLY_THEMES/);
  assert.doesNotMatch(context, /"system"/, "Monoform has no system mode");
  assert.doesNotMatch(context, /matchMedia|prefers-color-scheme/, "no OS-driven mode resolution");

  // `gray` is gone from the product, not merely unselectable: the token block
  // it named must not survive as a third palette nobody can reach.
  assert.doesNotMatch(context, /"gray"/, "the retired gray mode must not be reachable");
  assert.doesNotMatch(css, /data-appearance="gray"/, "no gray token block may remain");

  // Exactly two token blocks, and the default one is dark — an unstyled first
  // paint is what a missing :root block produces.
  assert.match(css, /:root,\s*\nhtml\[data-appearance="dark"\] \{/, "dark is the :root default");
  assert.match(css, /html\[data-appearance="light"\] \{/, "light defines its own tokens");

  // The context exposes one reader and one writer, and nothing else.
  assert.match(context, /appearance:\s*AppearanceMode;/);
  assert.match(context, /setAppearance:\s*\(appearance:\s*AppearanceMode\)\s*=>\s*void;/);
  assert.doesNotMatch(context, /\bsetTheme\b|\bsetMode\b|\bsetLayout\b/);

  // Whatever is written to the hub is the one theme name the hub still accepts.
  assert.match(context, /theme_name:\s*"monoform"/);
  const written = [...context.matchAll(/theme_name:\s*"([^"]+)"/g)].map((m) => m[1]);
  assert.deepEqual([...new Set(written)], ["monoform"]);
});

test("AppShell has exactly one primary navigation rendering path", () => {
  const shell = readFileSync(new URL("../components/shell/AppShell.tsx", import.meta.url), "utf8");
  const nav = readFileSync(new URL("../components/shell/navItems.tsx", import.meta.url), "utf8");

  // One rail, rendered once, for every viewport. A second (mobile) navigation
  // is exactly the drift this guard exists to catch.
  assert.equal(count(shell, /aria-label="Primary navigation"/g), 1, "exactly one primary nav landmark");
  assert.equal(count(shell, /<Rail\s*\/>/g), 1, "the rail is rendered exactly once");
  assert.equal(count(shell, /function Rail\(/g), 1, "there is one Rail implementation");
  assert.equal(count(shell, /function RailLink\(/g), 1, "there is one nav-item renderer");
  assert.equal(count(shell, /NAV_ITEMS\.\w+\(/g), 1, "NAV_ITEMS is consumed in exactly one expression");
  assert.doesNotMatch(shell, /\buseState\b|\bdata-layout\b|\buseDesign\b/, "the shell must not branch on a preference");

  // Every declared nav entry actually reaches the rail: the rail renders the
  // "workspace" and "manage" groups, so an item in any other group would be
  // silently dropped.
  const rendered = new Set(
    [...shell.matchAll(/item\.group === "([^"]+)"/g)].map((m) => m[1]),
  );
  assert.deepEqual([...rendered].sort(), ["manage", "workspace"], "the rail renders both groups");

  const items = [...nav.matchAll(/\{\s*href: "([^"]+)", label: "([^"]+)"[^}]*group: "([^"]+)"/g)];
  assert.ok(items.length >= 6, "NAV_ITEMS must still describe the app's routes");
  for (const [, href, label, group] of items) {
    assert.ok(rendered.has(group), `${label} (${href}) is in group "${group}", which the rail never renders`);
  }

  // Permission filtering happens once, before the split, so a scoped item is
  // hidden rather than rendered in a group that ignores scopes.
  assert.match(shell, /NAV_ITEMS\.filter\(\(item\) => !item\.scope \|\| hasScope\(item\.scope\)\)/);
});

function count(source, pattern) {
  return (source.match(pattern) ?? []).length;
}
