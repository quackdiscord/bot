import { useEffect, useState } from "react";

/**
 * usePresence keeps something mounted for `ms` after `open` turns false, so
 * it can play an exit animation. `closing` is true during that window.
 */
export function usePresence(open: boolean, ms = 160): { mounted: boolean; closing: boolean } {
  const [mounted, setMounted] = useState(open);
  if (open && !mounted) setMounted(true);

  useEffect(() => {
    if (open || !mounted) return;
    const timer = window.setTimeout(() => setMounted(false), ms);
    return () => window.clearTimeout(timer);
  }, [open, mounted, ms]);

  return { mounted: mounted || open, closing: mounted && !open };
}
