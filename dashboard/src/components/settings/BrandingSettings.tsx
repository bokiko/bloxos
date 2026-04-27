"use client";

// Phase 10 — branding settings panel (admin only).
//
// Three sections: text (title + subtitle), logo upload, favicon upload.
// Uploads are PNG-only with size caps enforced server-side. The server
// returns a fresh BrandingConfig after every write so the UI reflects the
// new SHA without needing to re-fetch.

import { useState, useRef, ChangeEvent } from "react";
import { Upload, Trash2 } from "lucide-react";
import { useBranding } from "@/contexts/BrandingContext";
import { useAuth } from "@/contexts/AuthContext";
import { useToast } from "@/components/Toast";
import { HUB_URL } from "@/lib/session";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

const MAX_LOGO_BYTES = 500 * 1024;
const MAX_FAVICON_BYTES = 100 * 1024;

export function BrandingSettings() {
  const { branding, refresh, logoUrl, faviconUrl } = useBranding();
  const { authFetch } = useAuth();
  const { addToast } = useToast();

  const [title, setTitle] = useState(branding.title);
  const [subtitle, setSubtitle] = useState(branding.subtitle);
  const [savingText, setSavingText] = useState(false);

  const handleSaveText = async () => {
    setSavingText(true);
    try {
      const res = await authFetch(`${HUB_URL}/api/branding`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title, subtitle }),
      });
      if (!res.ok) {
        const err = await res.json().catch(() => ({}));
        addToast("error", err.error || "Failed to save branding text");
        return;
      }
      await refresh();
      addToast("success", "Branding text saved");
    } catch {
      addToast("error", "Network error saving branding text");
    } finally {
      setSavingText(false);
    }
  };

  return (
    <div className="space-y-8">
      <section className="rounded-xl border border-blox-border bg-blox-card p-5">
        <h2 className="text-sm font-semibold text-blox-text mb-1">Text</h2>
        <p className="text-xs text-blox-muted mb-4">
          Title shows in the header and browser tab. Subtitle appears beneath
          the title on the login screen.
        </p>
        <div className="space-y-3 max-w-md">
          <div>
            <label className="block text-xs text-blox-muted mb-1.5 font-medium">
              Title
            </label>
            <Input
              type="text"
              value={title}
              maxLength={100}
              onChange={(e) => setTitle(e.target.value)}
              className="bg-blox-bg border-blox-border text-blox-text h-9 text-sm"
              placeholder="BloxOS"
            />
          </div>
          <div>
            <label className="block text-xs text-blox-muted mb-1.5 font-medium">
              Subtitle
            </label>
            <Input
              type="text"
              value={subtitle}
              maxLength={200}
              onChange={(e) => setSubtitle(e.target.value)}
              className="bg-blox-bg border-blox-border text-blox-text h-9 text-sm"
              placeholder="Fleet Management Dashboard"
            />
          </div>
          <div className="pt-1">
            <Button onClick={handleSaveText} disabled={savingText} size="sm">
              {savingText ? "Saving…" : "Save"}
            </Button>
          </div>
        </div>
      </section>

      <ImageUploader
        label="Logo"
        kind="logo"
        description={`PNG up to ${Math.round(MAX_LOGO_BYTES / 1024)} KB. Renders in the header and login screen.`}
        previewUrl={logoUrl}
        previewAlt={branding.title || "Logo"}
        previewClass="h-14"
        maxBytes={MAX_LOGO_BYTES}
        onChanged={refresh}
      />

      <ImageUploader
        label="Favicon"
        kind="favicon"
        description={`PNG up to ${Math.round(MAX_FAVICON_BYTES / 1024)} KB. Shown in the browser tab.`}
        previewUrl={faviconUrl}
        previewAlt="Favicon"
        previewClass="h-10 w-10"
        maxBytes={MAX_FAVICON_BYTES}
        onChanged={refresh}
      />
    </div>
  );
}

interface ImageUploaderProps {
  label: string;
  kind: "logo" | "favicon";
  description: string;
  previewUrl: string | null;
  previewAlt: string;
  previewClass: string;
  maxBytes: number;
  onChanged: () => Promise<void>;
}

function ImageUploader({
  label,
  kind,
  description,
  previewUrl,
  previewAlt,
  previewClass,
  maxBytes,
  onChanged,
}: ImageUploaderProps) {
  const { authFetch } = useAuth();
  const { addToast } = useToast();
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [busy, setBusy] = useState(false);

  const onSelect = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    if (file.type !== "image/png") {
      addToast("error", `${label} must be a PNG`);
      e.target.value = "";
      return;
    }
    if (file.size > maxBytes) {
      addToast("error", `${label} exceeds ${Math.round(maxBytes / 1024)} KB`);
      e.target.value = "";
      return;
    }
    setBusy(true);
    try {
      const fd = new FormData();
      fd.append("file", file);
      const res = await authFetch(`${HUB_URL}/api/branding/${kind}`, {
        method: "POST",
        body: fd,
      });
      if (!res.ok) {
        const err = await res.json().catch(() => ({}));
        addToast("error", err.error || `Failed to upload ${label.toLowerCase()}`);
        return;
      }
      await onChanged();
      addToast("success", `${label} updated`);
    } catch {
      addToast("error", `Network error uploading ${label.toLowerCase()}`);
    } finally {
      setBusy(false);
      if (inputRef.current) inputRef.current.value = "";
    }
  };

  const onRemove = async () => {
    setBusy(true);
    try {
      const res = await authFetch(`${HUB_URL}/api/branding/${kind}`, {
        method: "DELETE",
      });
      if (!res.ok) {
        const err = await res.json().catch(() => ({}));
        addToast("error", err.error || `Failed to remove ${label.toLowerCase()}`);
        return;
      }
      await onChanged();
      addToast("success", `${label} removed`);
    } catch {
      addToast("error", `Network error removing ${label.toLowerCase()}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="rounded-xl border border-blox-border bg-blox-card p-5">
      <h2 className="text-sm font-semibold text-blox-text mb-1">{label}</h2>
      <p className="text-xs text-blox-muted mb-4">{description}</p>

      <div className="flex items-center gap-4">
        <div
          className={cn(
            "flex items-center justify-center rounded-md border border-blox-border bg-blox-bg overflow-hidden",
            previewClass
          )}
        >
          {previewUrl ? (
            // eslint-disable-next-line @next/next/no-img-element -- user-uploaded blob from hub
            <img
              src={previewUrl}
              alt={previewAlt}
              className="max-h-full max-w-full object-contain"
            />
          ) : (
            <span className="text-[10px] text-blox-muted px-2">none</span>
          )}
        </div>

        <div className="flex items-center gap-2">
          <input
            ref={inputRef}
            type="file"
            accept="image/png"
            onChange={onSelect}
            className="hidden"
          />
          <Button
            size="sm"
            onClick={() => inputRef.current?.click()}
            disabled={busy}
          >
            <Upload className="w-3.5 h-3.5 mr-1.5" />
            {busy ? "Working…" : "Upload"}
          </Button>
          {previewUrl && (
            <Button
              size="sm"
              variant="outline"
              onClick={onRemove}
              disabled={busy}
            >
              <Trash2 className="w-3.5 h-3.5 mr-1.5" />
              Remove
            </Button>
          )}
        </div>
      </div>
    </section>
  );
}
