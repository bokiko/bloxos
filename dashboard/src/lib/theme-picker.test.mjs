import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// Monoform replaced the theme gallery with a single appearance control. The
// assertions below are the same contract as before — the picker must offer the
// complete set the registry exports, and must stay accessible — restated for
// the one control that survives.

test("appearance picker offers the complete set of appearance modes", () => {
  const registry = readFileSync(new URL("../contexts/ThemeContext.tsx", import.meta.url), "utf8");
  const preferences = readFileSync(
    new URL("../components/settings/PreferencesSettings.tsx", import.meta.url),
    "utf8",
  );
  const modes = registry.match(/export type AppearanceMode\s*=\s*([^;]+);/);
  assert.ok(modes, "appearance modes must be exported for consumers");
  for (const mode of ["gray", "dark"]) {
    assert.ok(modes[1].includes(`"${mode}"`), `missing ${mode}`);
    assert.ok(preferences.includes(`"${mode}"`), `picker does not offer ${mode}`);
  }
  // No light or system mode survives the Monoform reset.
  assert.doesNotMatch(modes[1], /"light"|"system"/);
  assert.match(preferences, /aria-pressed=\{appearance === option\}/);
});

test("appearance is chosen in one place only", () => {
  const menu = readFileSync(new URL("../components/UserMenu.tsx", import.meta.url), "utf8");
  // The account menu must not carry a competing appearance/theme picker.
  assert.doesNotMatch(menu, /THEME_NAMES|THEMES|setTheme|setAppearance|useDesign/);
  // ...but it must keep its permission-gated user management entry.
  assert.match(menu, /hasScope\("users\.admin"\)/);
});

test("the shell owns the header, and it stays usable on narrow screens", () => {
  const shell = readFileSync(new URL("../components/shell/AppShell.tsx", import.meta.url), "utf8");
  const css = readFileSync(new URL("../app/monoform.css", import.meta.url), "utf8");
  const page = readFileSync(new URL("../app/page.tsx", import.meta.url), "utf8");

  // Branding, page title and the global actions belong to the shell now, so
  // there is one header to keep responsive instead of one per route.
  assert.match(shell, /<header className="mf-topbar">/);
  assert.match(page, /<AppShell>/);
  assert.doesNotMatch(page, /<header\b/, "the overview must not draw a second header");

  // Below 700px the rail stops being a column beside the content and becomes a
  // scrollable strip above it, and the top bar drops its button labels — so
  // navigation and actions are never squeezed onto one crowded line.
  const narrow = css.match(/@media \(max-width: 700px\) \{([\s\S]*?)\n  \}/);
  assert.ok(narrow, "monoform.css must keep the narrow-screen breakpoint");
  assert.match(narrow[1], /\.mf-shell \{[^}]*display: block;/);
  assert.match(narrow[1], /\.mf-rail \{[^}]*flex-direction: row;/);
  assert.match(narrow[1], /\.mf-rail \{[^}]*overflow-x: auto;/);
  assert.match(narrow[1], /\.mf-topbar \.mf-utility-label \{[^}]*display: none;/);
});
