"use client";

// The electricity tariff the Overview's fleet power pane costs energy at.
//
// WHY IT LIVES HERE AND NOT ON THE PANE
// It used to be a number field and a currency select sitting inside the pane,
// under the chart. A tariff is typed once — when a contract changes, maybe
// once a year — and read on every visit, so a control for it was permanently
// occupying the most valuable strip of the Overview to do nothing. The pane
// still SHOWS the resulting cost; only the input moved.
//
// The value is unchanged and so is its storage: lib/workspace-prefs.mjs, keyed
// per user in localStorage, reached through the same useWorkspacePrefs hook
// the Overview uses. This is a relocation, not a migration — an operator who
// had already entered a rate finds it here.

import { useState } from "react";

import { MF_INPUT } from "@/lib/monoform-classes";
import { POWER_CURRENCIES, POWER_MAX_RATE } from "@/lib/workspace-prefs.mjs";
import { useWorkspacePrefs } from "@/components/overview/useWorkspacePrefs";

export function PowerRateSettings() {
  const { powerRate, setPowerRate } = useWorkspacePrefs();

  // While the field is being typed in, its own text wins: "0." must survive
  // the keystroke, and parsing it back to 0 and re-rendering "0" would eat the
  // decimal point. `null` means "follow the stored rate", which is the state
  // the field returns to on blur — so a rate changed elsewhere (a different
  // user signing in) is picked up without an effect that fights the cursor.
  const [draft, setDraft] = useState<string | null>(null);
  const text = draft ?? (powerRate.per_kwh === null ? "" : String(powerRate.per_kwh));

  const commit = (next: string) => {
    setDraft(next);
    const value = Number.parseFloat(next);
    setPowerRate({
      currency: powerRate.currency,
      // An unusable entry stores null, not 0: zero is a price ("my power is
      // free"), and claiming it would print a confident cost of nothing under
      // a chart of real watts.
      per_kwh: next.trim() !== "" && Number.isFinite(value) ? value : null,
    });
  };

  return (
    <section className="mf-panel p-5" aria-labelledby="power-rate-heading">
      <h2 id="power-rate-heading" className="text-sm font-semibold text-blox-text">
        Power rate
      </h2>
      <p className="mt-1 text-xs text-blox-muted">
        What a kilowatt-hour costs you. Until it is set, fleet power shows energy without a price —
        there is deliberately no default rate to invent one from.
      </p>
      <div className="mt-4 flex flex-wrap items-center gap-2">
        <input
          id="mf-power-rate"
          type="number"
          inputMode="decimal"
          min={0}
          max={POWER_MAX_RATE}
          step="0.001"
          placeholder="0.00"
          aria-label="Cost per kilowatt-hour"
          value={text}
          onChange={(event) => commit(event.target.value)}
          onBlur={() => setDraft(null)}
          className={`${MF_INPUT} mf-metric w-[110px] border px-2.5 text-right`}
        />
        <select
          aria-label="Currency"
          value={powerRate.currency}
          onChange={(event) => setPowerRate({ currency: event.target.value, per_kwh: powerRate.per_kwh })}
          className={`${MF_INPUT} border px-2`}
        >
          {(POWER_CURRENCIES as string[]).map((code) => (
            <option key={code} value={code}>
              {code}
            </option>
          ))}
        </select>
        <span className="text-xs text-blox-muted">per kWh</span>
      </div>
    </section>
  );
}
