import { type ReactNode, useState } from "react";

import { Button } from "./Button";
import { Dialog } from "./Dialog";
import { Field, TextArea } from "./Field";
import { InlineError } from "./States";

/**
 * ConfirmDialog asks before a consequential action, optionally collecting a
 * reason. Errors from onConfirm show inline so the user can fix and retry.
 */
export function ConfirmDialog({
  open,
  onClose,
  title,
  description,
  confirmLabel,
  tone = "primary",
  reason,
  pending,
  error,
  onConfirm,
  children,
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  description?: ReactNode;
  confirmLabel: string;
  tone?: "primary" | "danger" | "success";
  /** Adds a reason box. */
  reason?: { label: string; required?: boolean; placeholder?: string; hint?: string };
  pending?: boolean;
  error?: string | null;
  onConfirm: (reason: string) => void;
  children?: ReactNode;
}) {
  const [text, setText] = useState("");
  const blocked = Boolean(reason?.required && text.trim() === "");
  const close = () => {
    setText("");
    onClose();
  };
  const hasBody = Boolean(children || reason || error);
  return (
    <Dialog
      open={open}
      onClose={close}
      size="sm"
      title={title}
      description={description}
      onSubmit={() => {
        if (!blocked) onConfirm(text.trim());
      }}
      footer={
        <>
          <Button variant="ghost" onClick={close}>
            Cancel
          </Button>
          <Button type="submit" variant={tone} disabled={blocked} pending={pending}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      {hasBody ? (
        <>
          {children}
          {reason ? (
            <Field label={reason.label} required={reason.required} hint={reason.hint}>
              {(id) => (
                <TextArea
                  id={id}
                  rows={3}
                  autoFocus
                  value={text}
                  placeholder={reason.placeholder}
                  onChange={(e) => setText(e.currentTarget.value)}
                />
              )}
            </Field>
          ) : null}
          {error ? <InlineError>{error}</InlineError> : null}
        </>
      ) : null}
    </Dialog>
  );
}
