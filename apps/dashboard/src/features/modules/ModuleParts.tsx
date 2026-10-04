import { useMutation, useQueryClient } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";

import { ApiError, api, unwrap } from "~/api/client";
import { keys } from "~/api/queries";
import { Badge } from "~/ui/Badge";
import { Switch } from "~/ui/Field";
import { QuackIcon, type QuackIconName } from "~/ui/QuackIcon";
import { toast } from "~/ui/Toast";

import { type EnableProblem, enableProblem, type ModuleKey } from "./enable";
import s from "./ModuleParts.module.css";

/**
 * useModuleSwitch turns a module on or off through the settings API, which
 * re-runs the module's /setup checks against live Discord before switching
 * it on. A refusal is kept as problem to show next to the switch.
 */
export function useModuleSwitch(guildId: string, module: ModuleKey, name: string) {
  const queryClient = useQueryClient();
  const [problem, setProblem] = useState<EnableProblem | null>(null);
  const mutation = useMutation({
    mutationFn: (on: boolean) =>
      unwrap(
        api.PATCH("/guilds/{discordGuildID}/settings", {
          params: { path: { discordGuildID: guildId } },
          body: { [`${module}_enabled`]: on },
        }),
      ),
    onMutate: () => setProblem(null),
    onSuccess: (_, on) => {
      toast.success(on ? `${name} is on.` : `${name} is off.`);
      void queryClient.invalidateQueries({ queryKey: keys.modules(guildId) });
      void queryClient.invalidateQueries({ queryKey: keys.settings(guildId) });
    },
    onError: (e) =>
      setProblem(
        enableProblem(e instanceof ApiError ? e.message : `Couldn't switch ${name.toLowerCase()}.`),
      ),
  });
  return { toggle: mutation.mutate, pending: mutation.isPending, problem };
}

/**
 * ModuleHeader introduces a module: what it does in a sentence or two,
 * whether it's on, and the switch. A refused switch explains itself
 * underneath, pointing at /setup in Discord when the module was never set
 * up.
 */
export function ModuleHeader({
  icon,
  title,
  children,
  enabled,
  onToggle,
  pending,
  canManage,
  locked,
  problem,
  setupCommand,
}: {
  icon: QuackIconName;
  title: string;
  /** One or two plain sentences on what the module does. */
  children: ReactNode;
  enabled: boolean | undefined;
  onToggle: (on: boolean) => void;
  pending?: boolean;
  canManage: boolean;
  /** Why the switch is unavailable right now, such as unsaved changes. */
  locked?: string | null;
  problem?: EnableProblem | null;
  setupCommand: string;
}) {
  return (
    <div className={s.header}>
      <div className={s.top}>
        <span className={s.iconTile} data-on={enabled || undefined}>
          <QuackIcon name={icon} size={32} />
        </span>
        <div className={s.text}>
          <div className={s.titleRow}>
            <h2 className={s.title}>{title}</h2>
            {enabled === undefined ? null : enabled ? (
              <Badge tone="success" dot>
                On
              </Badge>
            ) : (
              <Badge tone="neutral" dot>
                Off
              </Badge>
            )}
          </div>
          <p className={s.description}>{children}</p>
          {locked ? <p className={s.locked}>{locked}</p> : null}
        </div>
        {canManage && enabled !== undefined ? (
          <Switch
            label={enabled ? `Turn off ${title}` : `Turn on ${title}`}
            checked={enabled}
            disabled={pending || Boolean(locked)}
            onChange={onToggle}
          />
        ) : null}
      </div>
      {problem ? (
        problem.needsSetup ? (
          <Callout tone="warning" icon="settings" title={`Set up ${title.toLowerCase()} first`}>
            Run <Command>{setupCommand}</Command> in your server. Quack picks or creates the
            channels it needs, checks its permissions, and turns {title.toLowerCase()} on.
          </Callout>
        ) : (
          <Callout
            tone="danger"
            icon="error"
            title={`Quack couldn't turn ${title.toLowerCase()} on`}
          >
            {problem.message} Fix it below, or run <Command>{setupCommand}</Command> in Discord to
            let Quack set it up for you.
          </Callout>
        )
      ) : null}
    </div>
  );
}

/** Callout is a banner that needs the reader's attention. */
export function Callout({
  tone,
  icon,
  title,
  children,
  action,
}: {
  tone: "danger" | "warning" | "info" | "success";
  icon: QuackIconName;
  title: ReactNode;
  children?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div role={tone === "danger" ? "alert" : undefined} data-tone={tone} className={s.callout}>
      <QuackIcon name={icon} size={24} />
      <div className={s.calloutText}>
        <p className={s.calloutTitle}>{title}</p>
        {children ? <p className={s.calloutBody}>{children}</p> : null}
      </div>
      {action ? <div className={s.calloutAction}>{action}</div> : null}
    </div>
  );
}

/** Command shows a Discord slash command inline, like a code span. */
export function Command({ children }: { children: ReactNode }) {
  return <code className={s.command}>{children}</code>;
}

/** StatTiles is a row of small counters. */
export function StatTiles({
  items,
}: {
  items: { label: string; value: number | string; tone?: "danger" | "warning"; hint?: string }[];
}) {
  return (
    <dl className={s.tiles}>
      {items.map((item) => (
        <div key={item.label} title={item.hint} className={s.tile}>
          <dt className={s.tileLabel}>{item.label}</dt>
          <dd className={s.tileValue} data-tone={item.tone}>
            {typeof item.value === "number" ? item.value.toLocaleString() : item.value}
          </dd>
        </div>
      ))}
    </dl>
  );
}
