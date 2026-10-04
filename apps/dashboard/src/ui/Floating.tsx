import { type ReactNode, type RefObject, useLayoutEffect, useRef, useState } from "react";

import { cx } from "~/lib/cx";

import s from "./Floating.module.css";

type Place = { top?: number; bottom?: number; left: number; width: number; maxHeight: number };

const GAP = 6;
const MAX = 320;

/**
 * Floating shows a panel under its anchor in the browser's top layer (the
 * popover API), so dialogs and scrolling containers never clip it. It flips
 * above the anchor when there isn't room below.
 */
export function Floating({
  anchor,
  open,
  children,
  className,
}: {
  anchor: RefObject<HTMLElement | null>;
  open: boolean;
  children: ReactNode;
  className?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [place, setPlace] = useState<Place | null>(null);

  useLayoutEffect(() => {
    const el = ref.current;
    const target = anchor.current;
    if (!open || !el || !target) return;
    // Track the anchor every frame while open: it can move without a scroll
    // or resize event, for example while a dialog animates in.
    let last = "";
    let frame = 0;
    const measure = () => {
      const r = target.getBoundingClientRect();
      const key = `${r.top}:${r.bottom}:${r.left}:${r.width}:${window.innerHeight}`;
      if (key === last) return;
      last = key;
      const below = window.innerHeight - r.bottom - GAP * 2;
      const above = r.top - GAP * 2;
      const flip = below < Math.min(MAX, 200) && above > below;
      setPlace(
        flip
          ? {
              bottom: window.innerHeight - r.top + GAP,
              left: r.left,
              width: r.width,
              maxHeight: Math.min(MAX, above),
            }
          : { top: r.bottom + GAP, left: r.left, width: r.width, maxHeight: Math.min(MAX, below) },
      );
    };
    const loop = () => {
      measure();
      frame = requestAnimationFrame(loop);
    };
    loop();
    if (!el.matches(":popover-open")) el.showPopover();
    return () => {
      cancelAnimationFrame(frame);
      if (el.matches(":popover-open")) el.hidePopover();
    };
  }, [open, anchor]);

  if (!open) return null;
  return (
    <div
      ref={ref}
      popover="manual"
      className={cx(s.floating, className)}
      style={place ?? { visibility: "hidden" }}
    >
      {children}
    </div>
  );
}
