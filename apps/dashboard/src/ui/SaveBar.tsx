import { useBlocker } from "@tanstack/react-router";

import { Button } from "./Button";
import { ConfirmDialog } from "./ConfirmDialog";
import s from "./SaveBar.module.css";

/**
 * SaveBar floats at the bottom of the page while a form has unsaved edits.
 * While it shows, leaving the page asks first so edits are never lost by
 * accident. Put it last in the page so it sticks to the bottom of the
 * scroll area.
 */
export function SaveBar({
  visible,
  onReset,
  onSave,
  pending,
  error,
  saveDisabled,
}: {
  visible: boolean;
  onReset: () => void;
  onSave: () => void;
  pending?: boolean;
  /** Why the last save failed, shown in place of the warning. */
  error?: string | null;
  /** Blocks saving, for example while a field is invalid. */
  saveDisabled?: boolean;
}) {
  const blocker = useBlocker({
    // Only leaving the page counts; search changes keep the form mounted.
    shouldBlockFn: ({ current, next }) => current.pathname !== next.pathname,
    enableBeforeUnload: visible,
    disabled: !visible,
    withResolver: true,
  });
  const blocked = blocker.status === "blocked";

  return (
    <>
      {/* The dock lets clicks through to the page; the dialog lives outside it. */}
      <div className={s.dock} aria-hidden={!visible || undefined}>
        <div
          role="region"
          aria-label="Unsaved changes"
          className={s.bar}
          data-visible={visible || undefined}
          data-nudged={blocked || undefined}
        >
          <p className={error && !blocked ? s.error : s.text}>
            {error && !blocked ? error : "You have unsaved changes."}
          </p>
          <div className={s.actions}>
            <Button variant="ghost" size="sm" onClick={onReset} disabled={pending || !visible}>
              Reset
            </Button>
            <Button
              size="sm"
              onClick={onSave}
              pending={pending}
              disabled={saveDisabled || !visible}
            >
              Save changes
            </Button>
          </div>
        </div>
      </div>
      <ConfirmDialog
        open={blocked}
        onClose={() => blocker.reset?.()}
        title="Leave without saving?"
        description="You have changes on this page that haven't been saved. If you leave now, they're gone."
        confirmLabel="Leave"
        tone="danger"
        onConfirm={() => blocker.proceed?.()}
      />
    </>
  );
}
