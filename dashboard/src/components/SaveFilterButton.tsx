"use client";

// Phase 11 — collapsed bookmark button that expands inline into a name
// input + save/cancel. POSTs to /api/me/filters via PreferencesContext.

import { useState, useRef, useEffect } from "react";
import { Bookmark, Check, X } from "lucide-react";
import { usePreferences } from "@/contexts/PreferencesContext";
import { useToast } from "@/components/Toast";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

interface SaveFilterButtonProps {
  currentFilter: Record<string, unknown>;
  disabled?: boolean;
}

export function SaveFilterButton({ currentFilter, disabled }: SaveFilterButtonProps) {
  const { saveFilter } = usePreferences();
  const { addToast } = useToast();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [saving, setSaving] = useState(false);
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (open) {
      // Defer focus so the input has actually mounted.
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [open]);

  const handleSave = async () => {
    const trimmed = name.trim();
    if (!trimmed) return;
    setSaving(true);
    try {
      await saveFilter(trimmed, currentFilter);
      addToast("success", `Saved filter “${trimmed}”`);
      setName("");
      setOpen(false);
    } catch (e) {
      addToast("error", e instanceof Error ? e.message : "Failed to save filter");
    } finally {
      setSaving(false);
    }
  };

  const handleCancel = () => {
    setName("");
    setOpen(false);
  };

  if (!open) {
    return (
      <Button
        size="sm"
        variant="outline"
        className="text-xs border-blox-border text-blox-text gap-1.5"
        onClick={() => setOpen(true)}
        disabled={disabled}
        title="Save current filter"
      >
        <Bookmark className="w-3 h-3" />
        Save filter
      </Button>
    );
  }

  return (
    <div className="flex items-center gap-1.5">
      <Input
        ref={inputRef}
        type="text"
        value={name}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            void handleSave();
          } else if (e.key === "Escape") {
            e.preventDefault();
            handleCancel();
          }
        }}
        placeholder="Filter name…"
        maxLength={60}
        className="h-8 w-40 text-xs bg-blox-bg border-blox-border text-blox-text"
      />
      <Button
        size="icon-sm"
        variant="default"
        onClick={() => void handleSave()}
        disabled={saving || !name.trim()}
        title="Save"
        aria-label="Save filter"
      >
        <Check className="w-3.5 h-3.5" />
      </Button>
      <Button
        size="icon-sm"
        variant="outline"
        onClick={handleCancel}
        disabled={saving}
        title="Cancel"
        aria-label="Cancel"
      >
        <X className="w-3.5 h-3.5" />
      </Button>
    </div>
  );
}
