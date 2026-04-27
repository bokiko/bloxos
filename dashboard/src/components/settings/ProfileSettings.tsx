"use client";

// Phase 11 — profile settings panel.
//
// Display name input + avatar upload/remove. Mutations route through
// PreferencesContext so the rest of the app (UserMenu trigger, settings
// page, anywhere the avatar is shown) updates in lockstep.

import { useRef, useState, ChangeEvent } from "react";
import { Upload, Trash2 } from "lucide-react";
import { usePreferences } from "@/contexts/PreferencesContext";
import { useToast } from "@/components/Toast";
import { Avatar } from "@/components/Avatar";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const MAX_AVATAR_BYTES = 200 * 1024;

export function ProfileSettings() {
  const { preferences, updateScalar, uploadAvatar, removeAvatar, myAvatarURL } = usePreferences();
  const { addToast } = useToast();
  const [displayName, setDisplayName] = useState(preferences.display_name);
  const [savingName, setSavingName] = useState(false);
  const [busyAvatar, setBusyAvatar] = useState(false);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  const handleSaveName = async () => {
    setSavingName(true);
    try {
      await updateScalar({ display_name: displayName });
      addToast("success", "Display name saved");
    } catch (e) {
      addToast("error", e instanceof Error ? e.message : "Failed to save display name");
    } finally {
      setSavingName(false);
    }
  };

  const handleSelectAvatar = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    if (file.type !== "image/png") {
      addToast("error", "Avatar must be a PNG");
      e.target.value = "";
      return;
    }
    if (file.size > MAX_AVATAR_BYTES) {
      addToast("error", `Avatar exceeds ${Math.round(MAX_AVATAR_BYTES / 1024)} KB`);
      e.target.value = "";
      return;
    }
    setBusyAvatar(true);
    try {
      await uploadAvatar(file);
      addToast("success", "Avatar updated");
    } catch (err) {
      addToast("error", err instanceof Error ? err.message : "Failed to upload avatar");
    } finally {
      setBusyAvatar(false);
      if (fileInputRef.current) fileInputRef.current.value = "";
    }
  };

  const handleRemoveAvatar = async () => {
    setBusyAvatar(true);
    try {
      await removeAvatar();
      addToast("success", "Avatar removed");
    } catch (err) {
      addToast("error", err instanceof Error ? err.message : "Failed to remove avatar");
    } finally {
      setBusyAvatar(false);
    }
  };

  return (
    <div className="space-y-8">
      <section className="rounded-xl border border-blox-border bg-blox-card p-5">
        <h2 className="text-sm font-semibold text-blox-text mb-1">Display name</h2>
        <p className="text-xs text-blox-muted mb-4">
          Shown in your account menu and as the fallback for your avatar
          initials. Up to 60 characters.
        </p>
        <div className="space-y-3 max-w-md">
          <Input
            type="text"
            value={displayName}
            maxLength={60}
            onChange={(e) => setDisplayName(e.target.value)}
            className="bg-blox-bg border-blox-border text-blox-text h-9 text-sm"
            placeholder="Your name"
          />
          <div className="pt-1">
            <Button onClick={handleSaveName} disabled={savingName} size="sm">
              {savingName ? "Saving…" : "Save"}
            </Button>
          </div>
        </div>
      </section>

      <section className="rounded-xl border border-blox-border bg-blox-card p-5">
        <h2 className="text-sm font-semibold text-blox-text mb-1">Avatar</h2>
        <p className="text-xs text-blox-muted mb-4">
          PNG up to {Math.round(MAX_AVATAR_BYTES / 1024)} KB. When unset, we
          render your initials over a colour derived from your display name.
        </p>
        <div className="flex items-center gap-4">
          <Avatar url={myAvatarURL} name={preferences.display_name} size={80} />
          <div className="flex items-center gap-2">
            <input
              ref={fileInputRef}
              type="file"
              accept="image/png"
              onChange={handleSelectAvatar}
              className="hidden"
            />
            <Button
              size="sm"
              onClick={() => fileInputRef.current?.click()}
              disabled={busyAvatar}
            >
              <Upload className="w-3.5 h-3.5 mr-1.5" />
              {busyAvatar ? "Working…" : "Upload"}
            </Button>
            {preferences.has_avatar && (
              <Button
                size="sm"
                variant="outline"
                onClick={handleRemoveAvatar}
                disabled={busyAvatar}
              >
                <Trash2 className="w-3.5 h-3.5 mr-1.5" />
                Remove
              </Button>
            )}
          </div>
        </div>
      </section>
    </div>
  );
}
