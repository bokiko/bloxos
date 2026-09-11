"use client";

// Monoform — the Overview's own shortcut into the arrangement preference.
//
// Deliberately NOT a second settings system. It writes the same two fields,
// through the same context, that Settings → Preferences writes; Settings is
// the durable, discoverable home and this is the reversible change you make
// without leaving the page you are looking at. There is no drag, no resize and
// no pixel control: the operator chooses an intent and the grid recomposes.
//
// It lives in the page's intro rather than the top bar because the shell must
// not branch on a preference — it renders one rail and one header for every
// route, and a test enforces that it holds no state.

import Link from "next/link";
import { SlidersHorizontal } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { MF_MENU, MF_MENU_ITEM } from "@/lib/monoform-classes";
import { usePreferences, type OverviewLayout, type OverviewWidgets } from "@/contexts/PreferencesContext";
import { useToast } from "@/components/Toast";
import {
  DEFAULT_OVERVIEW_LAYOUT,
  DEFAULT_OVERVIEW_WIDGETS,
  isDefaultOverview,
} from "@/lib/overview-layout.mjs";

const ARRANGEMENTS: { value: OverviewLayout; label: string }[] = [
  { value: "machine-first", label: "Machine-first" },
  { value: "balanced", label: "Balanced" },
  { value: "power-focus", label: "Power focus" },
];

const MODULES: { key: keyof OverviewWidgets; label: string }[] = [
  { key: "availability", label: "Fleet availability" },
  { key: "attention", label: "Needs attention" },
  { key: "urgent_alert", label: "Most urgent alert" },
];

export function OverviewCustomizeMenu() {
  const { preferences, updateScalar, hubSupportsOverview } = usePreferences();
  const { addToast } = useToast();

  const save = (
    patch: Partial<Pick<typeof preferences, "overview_layout" | "overview_widgets">>,
  ) => {
    updateScalar(patch).catch((e: unknown) =>
      addToast("error", e instanceof Error ? e.message : "Could not save the overview choice"),
    );
  };

  const atDefault = isDefaultOverview(preferences.overview_layout, preferences.overview_widgets);
  const modulesHidden = preferences.overview_layout === "power-focus";

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <button
            type="button"
            className="mf-icon-button"
            aria-label="Customize overview"
            title={
              hubSupportsOverview
                ? "Customize overview"
                : "Customize overview — this hub cannot store the choice yet"
            }
            disabled={!hubSupportsOverview}
          >
            <SlidersHorizontal className="h-4 w-4" aria-hidden="true" />
          </button>
        }
      />
      <DropdownMenuContent align="end" className={`${MF_MENU} min-w-[230px]`}>
        <DropdownMenuLabel className="mf-kicker uppercase">Arrangement</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={preferences.overview_layout}
          onValueChange={(value) => save({ overview_layout: value as OverviewLayout })}
        >
          {ARRANGEMENTS.map((option) => (
            <DropdownMenuRadioItem key={option.value} value={option.value} className={MF_MENU_ITEM}>
              {option.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>

        <DropdownMenuSeparator className="bg-border-subtle" />

        <DropdownMenuLabel
          className="mf-kicker uppercase"
          title={modulesHidden ? "Power focus shows no modules; your choices are kept." : undefined}
        >
          Modules
        </DropdownMenuLabel>
        {MODULES.map(({ key, label }) => (
          <DropdownMenuCheckboxItem
            key={key}
            className={MF_MENU_ITEM}
            checked={preferences.overview_widgets[key]}
            // Base UI keeps a checkbox item's menu open, which is what you
            // want here: turning two modules off is one visit, not two.
            onCheckedChange={(checked) =>
              // The hub requires the whole object — a partial one cannot say
              // whether a missing key means "off" or "unchanged".
              save({ overview_widgets: { ...preferences.overview_widgets, [key]: checked } })
            }
          >
            {label}
          </DropdownMenuCheckboxItem>
        ))}

        <DropdownMenuSeparator className="bg-border-subtle" />

        <DropdownMenuItem
          className={MF_MENU_ITEM}
          disabled={atDefault}
          onClick={() =>
            save({
              overview_layout: DEFAULT_OVERVIEW_LAYOUT as OverviewLayout,
              overview_widgets: { ...DEFAULT_OVERVIEW_WIDGETS } as OverviewWidgets,
            })
          }
        >
          Reset to recommended
        </DropdownMenuItem>
        <DropdownMenuItem className={MF_MENU_ITEM} render={<Link href="/settings?tab=preferences" />}>
          All preferences…
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
