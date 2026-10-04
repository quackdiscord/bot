import { createLink } from "@tanstack/react-router";
import { type ComponentProps, forwardRef, type ReactNode } from "react";

import { cx } from "~/lib/cx";

import s from "./Button.module.css";
import { Spinner } from "./Spinner";

type Variant = "primary" | "secondary" | "danger" | "success" | "ghost" | "link";
type Size = "sm" | "md" | "lg";

type OwnProps = {
  variant?: Variant;
  size?: Size;
  /** Shows a spinner and blocks clicks while a request is in flight. */
  pending?: boolean;
  icon?: ReactNode;
  grow?: boolean;
};

export type ButtonProps = OwnProps & ComponentProps<"button">;

function classes({ variant = "primary", size = "md", grow }: OwnProps, className?: string) {
  return cx(s.button, s[size], s[variant], grow && s.grow, className);
}

/** Button is the dashboard's button: solid, rounded, and quick to react. */
export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant, size, pending, icon, grow, className, children, disabled, type = "button", ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      disabled={disabled || pending}
      aria-busy={pending || undefined}
      data-pending={pending || undefined}
      className={classes({ variant, size, grow }, className)}
      {...rest}
    >
      {pending ? (
        <span className={s.spinner}>
          <Spinner size={size === "sm" ? 14 : 16} />
        </span>
      ) : null}
      <span className={s.content}>
        {icon}
        {children}
      </span>
    </button>
  );
});

type AnchorButtonProps = OwnProps & ComponentProps<"a">;

const AnchorButton = forwardRef<HTMLAnchorElement, AnchorButtonProps>(function AnchorButton(
  { variant, size, icon, grow, className, children, pending: _pending, ...rest },
  ref,
) {
  return (
    <a ref={ref} className={classes({ variant, size, grow }, className)} {...rest}>
      <span className={s.content}>
        {icon}
        {children}
      </span>
    </a>
  );
});

/** ButtonLink is a router link that looks like a Button. */
export const ButtonLink = createLink(AnchorButton);

/** ExternalButton is a plain anchor that looks like a Button. */
export const ExternalButton = AnchorButton;
