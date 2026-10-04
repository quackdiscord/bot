import { X } from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useRef } from "react";

import { cx } from "~/lib/cx";
import { usePresence } from "~/lib/usePresence";

import s from "./Dialog.module.css";

/**
 * Dialog is a modal on the native <dialog> element, so focus trapping,
 * Escape, and the top layer come from the browser. It unmounts after its
 * exit animation, so forms inside reset between openings.
 */
export function Dialog({
  open,
  onClose,
  title,
  description,
  children,
  footer,
  onSubmit,
  size = "md",
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  footer?: ReactNode;
  /** Makes the body a form; Enter submits it. */
  onSubmit?: () => void;
  size?: "sm" | "md" | "lg";
}) {
  const { mounted, closing } = usePresence(open, 150);
  if (!mounted) return null;
  return (
    <DialogFrame
      closing={closing}
      onClose={onClose}
      title={title}
      description={description}
      footer={footer}
      onSubmit={onSubmit}
      size={size}
    >
      {children}
    </DialogFrame>
  );
}

function DialogFrame({
  closing,
  onClose,
  title,
  description,
  children,
  footer,
  onSubmit,
  size,
}: {
  closing: boolean;
  onClose: () => void;
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  footer?: ReactNode;
  onSubmit?: () => void;
  size: "sm" | "md" | "lg";
}) {
  const ref = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const dialog = ref.current;
    if (dialog && !dialog.open) dialog.showModal();
    return () => dialog?.close();
  }, []);

  const body = (
    <>
      <header className={s.header}>
        <h2 className={s.title}>{title}</h2>
        {description ? <p className={s.description}>{description}</p> : null}
        <button type="button" aria-label="Close" onClick={onClose} className={s.close}>
          <X size={18} />
        </button>
      </header>
      {children ? <div className={s.body}>{children}</div> : null}
      {footer ? <footer className={s.footer}>{footer}</footer> : null}
    </>
  );

  return (
    <dialog
      ref={ref}
      data-closing={closing || undefined}
      className={cx(s.dialog, s[size])}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onMouseDown={(e) => {
        // A press on the backdrop lands on the dialog element itself.
        if (e.target === e.currentTarget) onClose();
      }}
    >
      {onSubmit ? (
        <form
          className={s.frame}
          onSubmit={(e: FormEvent) => {
            e.preventDefault();
            onSubmit();
          }}
        >
          {body}
        </form>
      ) : (
        <div className={s.frame}>{body}</div>
      )}
    </dialog>
  );
}
