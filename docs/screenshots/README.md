# Dashboard gallery

BloxOS has one visual system, **Monoform**, with two contrast modes — **Gray**
(the default) and **Dark**. See the [design guide](../themes/README.md) for
what that means and where it lives.

> **These captures predate Monoform.** They show the **v1.1.0** application,
> which still offered selectable layouts and palettes. They are kept here
> because they are honest captures of a released version, not concept renders,
> but they are **not** what the current dashboard looks like. Refreshed
> Monoform captures are still to be taken; see the asset notes below for how.

The captures use three synthetic test machines. The demo has no GPU sensors
and no active AI sessions, so the UI truthfully shows N/A and zero sessions.
Names, readings and connection timing are illustrative, not hardware
benchmarks.

## v1.1.0 · open fleet overview

![The v1.1.0 dashboard with summary panels and Linux and Windows demo machines](https://cdn.jsdelivr.net/gh/bokiko/bloxos@bca13c456ecc1e5bd89282f96486a43b602bc4f9/docs/screenshots/operations-wall.png)

## v1.1.0 · sidebar and context column

![The v1.1.0 dashboard showing fleet analytics and a right-hand context column](https://cdn.jsdelivr.net/gh/bokiko/bloxos@bca13c456ecc1e5bd89282f96486a43b602bc4f9/docs/screenshots/grove-workspace.png)

## v1.1.0 · table-first view

![The v1.1.0 dashboard showing a machine table and telemetry panels](https://cdn.jsdelivr.net/gh/bokiko/bloxos@bca13c456ecc1e5bd89282f96486a43b602bc4f9/docs/screenshots/precision-console.png)

## Asset notes

- Captured from the release candidate whose source tree matches v1.1.0
  (`a46b39954616c8c1cb851412ee46fe05c816ab3e`).
- Images are unmodified browser captures, 1440 pixels wide. Originals remain
  version-controlled beside this file.
- Embedded images use commit-pinned jsDelivr copies of these public repository
  files because GitHub raw-image delivery returned intermittent 503 errors.
  When replacing captures, commit the new images
  first, then update the image URLs in this gallery and the root README to that
  commit. The URL pin identifies the stored files, not the app version captured.
- Only synthetic fixture machines are visible. No enrollment links, passwords,
  tokens, real network addresses or private machine names are included.
- The app's canonical [logo SVGs](../../dashboard/public/brand/) are reused by
  the root README; no separate lookalike logo is maintained here.
- When refreshing screenshots, use a disposable demo fleet, wait for the layout
  and fonts to settle, close menus, check all visible content for private data,
  and update this version note. Capture both contrast modes rather than
  presenting one as the whole system. Do not invent telemetry to hide
  unavailable sensors.

[Design guide](../themes/README.md) · [Back to BloxOS](../../README.md)
