# BloxOS design

## One visual system

BloxOS has a single visual system, **Monoform**. There is no layout picker, no
theme gallery and no palette-per-layout: every authenticated page renders the
same shell — a fixed left rail carrying the whole of the navigation, and a top
bar carrying the page title and the global actions.

The only appearance choice is contrast:

| Mode | Character |
| --- | --- |
| **Gray** (default) | Dark-gray canvas with slightly raised graphite panels and hairline borders. |
| **Dark** | The same system on a deeper, near-black ground for low-light rooms. |

Both modes are dark-family surfaces. There is no light mode and no
"follow the operating system" mode, so the first painted frame is never a
different colour from the one that follows it.

Choose **Settings → Preferences → Appearance**, or use the contrast toggle in
the top bar. The choice is stored locally so it applies before you log in, and
synced to your account through `GET/PATCH /api/me/theme` so it follows you to
another browser. A failed sync is not fatal: the local choice stays in effect.

## What replaced the layouts

Earlier releases offered five selectable dashboard layouts (Classic,
Operations Wall, Grove Workspace, Precision Console, Fleet Ledger), each with
its own colour variants, plus an eight-palette theme gallery. All of it has
been removed in favour of one system.

Accounts that chose a retired layout or palette are migrated automatically:
an existing **dark** choice becomes Monoform's Dark contrast mode, and
everything else becomes Gray. Nothing needs to be reset by hand, and the
per-user database columns the old system wrote are left in place rather than
dropped, so an older hub binary still reads its own rows.

Everything the layouts were used for is unchanged: live fleet data, the same
permissions and action paths, and the same status semantics. Empty or missing
telemetry is reported as unavailable, not as an invented zero; stale readings
are excluded from current aggregates; GPU power is component telemetry rather
than wall power, and incomplete readings are labelled partial.

## Where it lives

- `dashboard/src/app/monoform.css` — the whole token layer and every `.mf-*`
  component class, including both contrast modes.
- `dashboard/src/app/globals.css` — imports the above and maps the shared
  semantic tokens the component library reads.
- `dashboard/src/components/shell/AppShell.tsx` — the one shell: rail, top bar
  and content region.
- `dashboard/src/components/shell/navItems.tsx` — the single navigation list.
- `dashboard/src/contexts/ThemeContext.tsx` — `useTheme() → { appearance,
  setAppearance }`, local persistence and the per-user sync.
- `dashboard/src/app/layout.tsx` — the pre-hydration bootstrap that paints the
  stored contrast mode before React mounts.
- `dashboard/src/lib/monoform-classes.ts` — the shared class constants pages
  use instead of repeating utility strings.
- `hub/user_prefs.go` — server-side acceptance of `monoform` plus `gray`/`dark`.

Guard tests in `dashboard/src/lib/monoform-guard.test.mjs` fail if the retired
vocabulary reappears in dashboard source, or if a second navigation rendering
path is added.

## BloxOS logo

The shared square mark is in three parts: an open enclosure, a solid compute
core and a detached node. It inherits `currentColor`, so it takes the active
contrast mode's foreground wherever it appears.

SVG masters are in `dashboard/public/brand/`: `bloxos-mark.svg`,
`bloxos-mark-black.svg`, and `bloxos-mark-white.svg`. These have transparent
backgrounds. The React `BloxosMark` component is used by the default
application header; uploaded instance logos and custom titles retain their
existing precedence.

[Screenshot gallery](../screenshots/README.md) · [Back to BloxOS](../../README.md)
