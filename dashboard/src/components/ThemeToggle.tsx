"use client";

// Monoform — a direct two-state contrast switch. There is no dropdown, no
// light/system option and no palette picker.

import { Moon, Contrast } from "lucide-react";
import { useTheme } from "@/contexts/ThemeContext";
import { Button } from "@/components/ui/button";

export function ThemeToggle() {
  const { appearance, setAppearance } = useTheme();
  const next = appearance === "gray" ? "dark" : "gray";

  return (
    <Button
      type="button"
      variant="ghost"
      size="icon-sm"
      onClick={() => setAppearance(next)}
      aria-label={`Use ${next} appearance`}
      title={`Use ${next} appearance`}
      className="text-blox-muted hover:text-blox-text"
    >
      {appearance === "gray" ? (
        <Moon className="w-4 h-4" />
      ) : (
        <Contrast className="w-4 h-4" />
      )}
    </Button>
  );
}
