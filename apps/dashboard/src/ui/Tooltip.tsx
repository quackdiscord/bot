import { cloneElement, type ReactElement, type ReactNode, useId, useState } from "react";
import { createPortal } from "react-dom";

import s from "./Tooltip.module.css";

type Side = "right" | "top" | "bottom";

/**
 * Tooltip shows a label next to its child on hover or keyboard focus. It is
 * portaled and fixed, so scrolling containers never clip it.
 */
export function Tooltip({
  label,
  side = "top",
  children,
}: {
  label: ReactNode;
  side?: Side;
  children: ReactElement<{
    onMouseEnter?: (e: React.MouseEvent<HTMLElement>) => void;
    onMouseLeave?: () => void;
    onFocus?: (e: React.FocusEvent<HTMLElement>) => void;
    onBlur?: () => void;
    "aria-describedby"?: string;
  }>;
}) {
  const id = useId();
  const [at, setAt] = useState<{ x: number; y: number } | null>(null);

  const show = (el: HTMLElement) => {
    const r = el.getBoundingClientRect();
    if (side === "right") setAt({ x: r.right + 12, y: r.top + r.height / 2 });
    else if (side === "bottom") setAt({ x: r.left + r.width / 2, y: r.bottom + 8 });
    else setAt({ x: r.left + r.width / 2, y: r.top - 8 });
  };
  const hide = () => setAt(null);

  return (
    <>
      {cloneElement(children, {
        onMouseEnter: (e) => show(e.currentTarget),
        onMouseLeave: hide,
        onFocus: (e) => show(e.currentTarget),
        onBlur: hide,
        "aria-describedby": at ? id : undefined,
      })}
      {at
        ? createPortal(
            <div
              id={id}
              role="tooltip"
              data-side={side}
              className={s.tip}
              style={{ left: at.x, top: at.y }}
            >
              {label}
            </div>,
            document.body,
          )
        : null}
    </>
  );
}
