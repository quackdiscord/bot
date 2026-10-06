import { type ReactNode, useState } from "react";

import { ApiError } from "~/api/client";
import { useSignInAgain } from "~/api/session";
import { cx } from "~/lib/cx";

import { Button } from "./Button";
import { Logo } from "./Logo";
import { QuackIcon, type QuackIconName } from "./QuackIcon";
import s from "./States.module.css";

/** Empty explains why a list has nothing in it and what to do next. */
export function Empty({
  icon,
  title,
  children,
  action,
  compact,
}: {
  /** A Signals icon; without one the Quack logo shows. */
  icon?: QuackIconName;
  title: ReactNode;
  children?: ReactNode;
  action?: ReactNode;
  compact?: boolean;
}) {
  return (
    <div className={cx(s.empty, compact && s.compact)}>
      {icon ? (
        <QuackIcon name={icon} size={compact ? 32 : 44} />
      ) : (
        <Logo size={compact ? 40 : 60} />
      )}
      <div className={s.text}>
        <p className={s.title}>{title}</p>
        {children ? <p className={s.body}>{children}</p> : null}
      </div>
      {action}
    </div>
  );
}

/**
 * ErrorState shows a failed load with the server's message and a retry. A
 * server's 2FA requirement gets its own explanation instead.
 */
export function ErrorState({ error, retry }: { error: unknown; retry?: () => void }) {
  if (error instanceof ApiError && error.code === "mfa_required") return <MfaRequired />;
  const message =
    error instanceof ApiError
      ? error.message
      : "Something went wrong loading this. Try again in a moment.";
  const forbidden = error instanceof ApiError && error.status === 403;
  return (
    <Empty
      icon={forbidden ? "lock" : "error"}
      title={forbidden ? "You can't see this" : "Couldn't load this"}
      action={
        retry && !forbidden ? (
          <Button variant="secondary" size="sm" onClick={retry}>
            Try again
          </Button>
        ) : null
      }
    >
      {forbidden ? "Your Discord permissions in this server don't include this page." : message}
    </Empty>
  );
}

/**
 * MfaRequired explains that the server requires 2FA for moderation and
 * Quack hasn't confirmed the user has it. Quack only learns that when they
 * sign in, so the way back is to turn 2FA on, then sign in again.
 */
export function MfaRequired() {
  const signInAgain = useSignInAgain();
  const [pending, setPending] = useState(false);
  return (
    <Empty
      icon="lock"
      title="This server requires 2FA"
      action={
        <Button
          pending={pending}
          onClick={() => {
            setPending(true);
            void signInAgain();
          }}
        >
          Sign in again
        </Button>
      }
    >
      It requires two-factor authentication for moderation, so Quack does too. Turn on two-factor
      authentication in Discord, then sign out and back in.
    </Empty>
  );
}

/** InlineError is a failed action's message, shown where the action was. */
export function InlineError({ children }: { children: ReactNode }) {
  return (
    <p role="alert" className={s.inlineError}>
      {children}
    </p>
  );
}

/** Skeleton is a placeholder bar shown while content loads. */
export function Skeleton({
  width = "100%",
  height = 16,
  round,
}: {
  width?: number | string;
  height?: number;
  round?: boolean;
}) {
  return <span className={cx(s.skeleton, round && s.round)} style={{ width, height }} />;
}

/** SkeletonRows fills a list area while the first page loads. */
export function SkeletonRows({ rows = 6 }: { rows?: number }) {
  return (
    <div className={s.rows} aria-busy>
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className={s.row}>
          <Skeleton width={32} height={32} round />
          <div className={s.rowText}>
            <Skeleton width={`${40 + ((i * 17) % 35)}%`} height={12} />
            <Skeleton width={`${20 + ((i * 11) % 25)}%`} height={10} />
          </div>
        </div>
      ))}
    </div>
  );
}
