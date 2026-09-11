# The Overview

Two things are always on the Overview, in every arrangement:

- **Fleet power**, the anchor at the top. It never folds away.
- **Machine fleet**, the table you search, filter and act in, directly below.

Between them sit up to three compact context modules. You choose which, and
your choice is saved to your BloxOS account, so it follows you to any browser
you sign in from. Each user has their own — including viewers.

## Choosing an arrangement

**Settings → Preferences → Overview** is the durable home for the choice. The
sliders control on the Overview's own header opens the same settings without
leaving the page.

| Arrangement | What it does |
|---|---|
| **Machine-first** (default) | Power anchored on the left, the context modules compact beside it, the table high on the page. |
| **Balanced** | Every context module in one row, with power full-width beneath it. |
| **Power focus** | Power only. See the safety note below. |

There is no drag-and-drop and no resizing. You choose what matters to you; the
interface decides the geometry, so no combination can produce a broken layout.

## The context modules

| Module | What it shows | What its control does |
|---|---|---|
| **Fleet availability** | How many machines are connected, and how many are online, need review, or are offline. | Filters the machine table to exactly those machines and jumps to it. |
| **Needs attention** | How many alert rules are firing, split by critical and warning. | Opens the alerts panel. |
| **Most urgent alert** | The single highest-severity, most recent alert, with the machine it came from. | Opens that machine. |

Two of these count different things and can legitimately disagree. **Fleet
availability** is machine state, worked out from each machine's live readings —
a machine that has stopped reporting is "needs review" whether or not a rule
has fired. **Needs attention** is the hub's alert list, produced by the alert
rules in `docs/alerts.md`. A stale machine with no matching rule shows in one
and not the other, and neither is wrong.

That is also why their controls lead to different places. A module always takes
you to the data it was counting, never to a neighbouring list that might be
empty.

## Modules with nothing to report

A module that has nothing real to say is left out, and the row closes up around
it. **Most urgent alert** disappears entirely on a fleet with no active alerts,
rather than showing an empty card or holding a gap open.

**Fleet availability** and **Needs attention** stay visible at zero, drawn
quietly: a healthy fleet and a module that failed to load must not look the
same. At zero they are not clickable — a button that opens an empty list is a
promise the product cannot keep.

## Power focus still shows incidents

Power focus hides the context modules. It does not hide a problem: while any
machine is critical or offline, a compact marker stays above the chart with the
counts and a way into the affected machines. Hiding the summary never hides the
incident.

## Notes

- Turning a module off in Power focus is remembered — switching back to
  Machine-first restores exactly what you had.
- **Reset to recommended** returns the arrangement and all three modules to
  their defaults, and is disabled when you are already there.
- The arrangement and module choices are stored on your account
  (`overview_layout`, `overview_widgets`). The power window and electricity
  tariff are stored in the browser instead, per user — see
  `docs/power-history.md`.
- Against a hub older than this feature, the controls are visible but disabled
  and say so, rather than appearing to save a choice the hub cannot keep.
