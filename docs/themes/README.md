# BloxOS designs and colors

## Live application

Choose **Account menu → Design** or **Settings → Theme → Design**. Select a
layout, then **Original**, **Bright**, or **Dark**. Each layout remembers its own
color. Original reproduces the design study's fixed palette; Bright and Dark
are explicit choices, not operating-system modes. There are nine combinations.

- **Operations Wall:** open summary panels, resource bars and a machine register.
- **Grove Workspace:** sidebar navigation, central analytics and a context rail.
- **Precision Console:** compact navigation, a table-first fleet and instruments.

The layouts use live fleet data and the existing permissions and action paths.
Selected navigation and colors continue onto machine details, inventory,
versions, AI sessions, users and settings. Those pages retain their working
contents and contextual controls. Empty/missing telemetry is unavailable, not
an invented zero; stale readings are excluded from current aggregates. GPU
power is component telemetry, not wall power, and incomplete readings are
explicitly labelled partial.

**Classic** retains the previous dashboard and all eight palettes below. Its
light/dark/system controls apply only to Classic. Existing accounts default to
Classic; selecting a new layout does not replace their old palette preference.

Design preferences are saved locally per account and synced through
`GET/PATCH /api/me/design`. The hub validates the four layout names and three
color choices, updating only the authenticated user's row. On a cold load a
neutral loading frame resolves the preference; a failed sync falls back to the
cached choice with a visible warning and does not block using the app. Failed
saves remain local and are reported instead of being presented as synced.

## Reference studies

The revised [interactive comparison](index.html) contains three structurally distinct
proposals built from the same sample fleet data:

- **Operations Wall:** modern rounded panels on an open canvas, balanced summary
  cards, an availability ring, slim resource bars and a compact machine register.
- **Grove Workspace:** persistent navigation, a central analytics column and a
  separate operational context rail; radial availability and machine list.
- **Precision Console:** narrow tool rail, horizontal navigation, compact status
  strip and a machine table with telemetry instruments below.

The HTML preview is a self-contained reference with illustrative data, not the
running application. It uses one shared data object for all three presentations.
The live implementation described above connects these compositions to BloxOS.

## Color variants

Each layout offers **Original**, **Bright**, and **Dark**, for nine combinations.
Original retains the approved colors without CSS overrides. Bright and Dark
change colors only, with scoped surface, text, border and chart treatments.

| Layout | Bright | Dark |
| --- | --- | --- |
| Operations Wall | Porcelain blue and white | Midnight blue |
| Grove Workspace | Botanical cream and sage | Deep evergreen |
| Precision Console | Cool technical white | Near-black graphite |

Use the color controls below the design selector. Each layout remembers its own
selection in browser storage when available. Links such as `#studio/bright` and
`#console/dark` open a specific combination. All variants use the same fleet data.

## Earlier palette implementation


Open [the interactive comparison](index.html) in a browser. Its three buttons show
identical illustrative fleet information in each design; it makes no API calls.
The comparison is self-contained, with its own layout styles.

| Design | Character | Visual treatment |
| --- | --- | --- |
| Mission Control | Bold indigo instrumentation | Square-edged tiles, cyan rules, large sans-serif numbers, separated status cells |
| Graphite | Tactile charcoal console | Sculpted panels, inset highlights, cast shadows, mint accents, monospaced readings |
| Verdant | Organic forest workspace | Broad curved panels, pill actions, lime fleet summary, lighter sans-serif figures |

In the application, choose **Classic → Theme** to select these older palettes.
All three are dark-only. The existing BloxOS default and the four other existing palette choices are
preserved. The selected design uses the existing local and per-user server
preferences, including the initial pre-hydration theme bootstrap.

The application retains its existing data, navigation, gauges, charts, filters,
permissions and status semantics. Named CSS hooks change fleet panel geometry,
metric typography, header treatment and spacing. Shared semantic color tokens
carry the palette through other screens, menus and dialogs. Warning and critical
fleet summaries keep their status surfaces rather than receiving the decorative
lime hero surface.

The references informed proportion, surface treatment and atmosphere. No source
image, third-party branding, financial widget, or unrelated CRM data is included.

## Implementation

- `dashboard/src/app/design-themes.css`: palettes and visual treatments.
- `dashboard/src/contexts/ThemeContext.tsx`: design registry and persistence.
- `dashboard/src/app/layout.tsx`: initial theme bootstrap.
- `dashboard/src/components/ThemePreview.tsx`: dashboard thumbnails in the picker.
- `hub/user_prefs.go`: server acceptance of the three new preference names.

The HTML comparison remains illustrative. Browser verification of the actual
application is the release gate, not merely a screenshot of this reference.

## BloxOS logo

The shared square mark develops the Precision Console symbol into three parts:
an open enclosure, a solid compute core and a detached node. The same geometry
appears in every layout and inherits the active palette's accent. Grove uses the
full lockup; Precision uses the icon in its tool rail and repeats it beside the
wordmark on mobile when that rail is hidden.

SVG masters are in `dashboard/public/brand/`: `bloxos-mark.svg`,
`bloxos-mark-black.svg`, and `bloxos-mark-white.svg`. These have transparent
backgrounds. The React `BloxosMark` component inherits `currentColor` and is used
by the default application header. Uploaded instance logos and custom titles
retain their existing precedence.
