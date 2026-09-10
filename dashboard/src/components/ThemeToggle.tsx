"use client";

// Monoform — a direct two-state theme switch. There is no dropdown, no system
// option and no palette picker: the product has a dark theme and a light one.

import { Moon, Sun } from "lucide-react";
import { useTheme } from "@/contexts/ThemeContext";
import { Button } from "@/components/ui/button";

const NEXT_LABEL = {
  dark: "Switch to light theme",
  light: "Switch to dark theme",
} as const;

export function ThemeToggle() {
  const { appearance, setAppearance } = useTheme();
  const next = appearance === "dark" ? "light" : "dark";
  const label = NEXT_LABEL[appearance];

  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-sm"
      onClick={() => setAppearance(next)}
      aria-label={label}
      title={label}
      className="text-blox-muted hover:text-blox-text"
    >
      {/* The icon names the theme the click would GET you, matching the label
          — an icon of the theme you are already in tells you nothing. */}
      {appearance === "dark" ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
    </Button>
  );
}
