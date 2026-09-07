# BloxOS layout studies

The revised [interactive comparison](index.html) contains three structurally distinct
proposals built from the same sample fleet data:

- **Operations Wall:** modern rounded panels on an open canvas, balanced summary
  cards, an availability ring, slim resource bars and a compact machine register.
- **Grove Workspace:** persistent navigation, a central analytics column and a
  separate operational context rail; radial availability and machine list.
- **Precision Console:** narrow tool rail, horizontal navigation, compact status
  strip and a machine table with telemetry instruments below.

These are design proposals, not integrated application layouts. The previous
implementation below changes palettes and styling only, and does not implement
the revised compositions. The HTML preview is self-contained, responsive, and
uses one shared data object for the three machine and resource presentations.

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

In the application, choose **Settings → Theme** to select a design. All three are
dark-only. The existing BloxOS default and the four other existing palette choices are
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

The HTML comparison is illustrative, not a replacement implementation of the
application. Browser visual verification remains required before release.

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
