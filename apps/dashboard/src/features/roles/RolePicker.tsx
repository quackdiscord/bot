import { useQuery } from "@tanstack/react-query";
import { Search, X } from "lucide-react";
import { useId, useRef, useState } from "react";

import { rolesQuery } from "~/api/directory";
import type { DirectoryRole } from "~/api/types";
import { cx } from "~/lib/cx";
import { TextInput } from "~/ui/Field";
import { Floating } from "~/ui/Floating";
import { Spinner } from "~/ui/Spinner";

import s from "./RolePicker.module.css";
import { pickableRoles, roleColor } from "./roles";

/**
 * RolePicker chooses any number of the server's roles. Picked roles show as
 * chips in Discord's role colors; a search box below adds more. A saved
 * role that no longer exists stays listed and reads as deleted, so nothing
 * changes silently.
 */
export function RolePicker({
  guildId,
  value,
  onChange,
  id,
  max,
  disabled,
  invalid,
  emptyLabel = "No roles",
  placeholder = "Add a role",
}: {
  guildId: string;
  /** The picked role IDs. */
  value: readonly string[];
  onChange: (roleIds: string[]) => void;
  id?: string;
  /** The most roles that can be picked; the search box hides at the limit. */
  max?: number;
  disabled?: boolean;
  invalid?: boolean;
  /** Shown instead of chips when nothing is picked and the picker is read-only. */
  emptyLabel?: string;
  placeholder?: string;
}) {
  const roles = useQuery(rolesQuery(guildId));
  const [text, setText] = useState("");
  const [open, setOpen] = useState(false);
  const [highlight, setHighlight] = useState(0);
  const listId = useId();
  const anchor = useRef<HTMLDivElement>(null);

  const byId = new Map((roles.data ?? []).map((r) => [r.id, r]));
  const options = pickableRoles(roles.data ?? [], value, text);
  const full = max !== undefined && value.length >= max;

  const add = (role: DirectoryRole) => {
    onChange([...value, role.id]);
    setText("");
    setHighlight(0);
  };
  const remove = (roleId: string) => onChange(value.filter((v) => v !== roleId));

  const showList = open && !disabled && roles.isSuccess;

  return (
    <div className={s.picker}>
      {value.length > 0 ? (
        <ul className={s.chips} aria-label="Picked roles">
          {value.map((roleId) => (
            <RoleChip
              key={roleId}
              roleId={roleId}
              role={byId.get(roleId)}
              loading={roles.isPending}
              onRemove={disabled ? undefined : () => remove(roleId)}
            />
          ))}
        </ul>
      ) : disabled ? (
        <p className={s.empty}>{emptyLabel}</p>
      ) : null}

      {disabled || full ? null : (
        <div ref={anchor}>
          <TextInput
            id={id}
            value={text}
            placeholder={roles.isError ? "Couldn't load roles" : placeholder}
            autoComplete="off"
            role="combobox"
            aria-expanded={showList}
            aria-controls={listId}
            invalid={invalid}
            disabled={roles.isError}
            leading={roles.isPending ? <Spinner size={14} /> : <Search size={16} />}
            onChange={(e) => {
              setText(e.currentTarget.value);
              setOpen(true);
              setHighlight(0);
            }}
            onFocus={() => setOpen(true)}
            onClick={() => setOpen(true)}
            onBlur={() => window.setTimeout(() => setOpen(false), 120)}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                setOpen(false);
                return;
              }
              if (e.key === "Backspace" && !text && value.length > 0) {
                remove(value[value.length - 1]!);
                return;
              }
              if (!options.length) return;
              if (e.key === "ArrowDown") {
                e.preventDefault();
                setOpen(true);
                setHighlight((h) => (h + 1) % options.length);
              } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setOpen(true);
                setHighlight((h) => (h - 1 + options.length) % options.length);
              } else if (e.key === "Enter") {
                e.preventDefault();
                const role = options[Math.min(highlight, options.length - 1)];
                if (role) add(role);
              }
            }}
          />
          <Floating anchor={anchor} open={showList}>
            {options.length ? (
              <ul id={listId} role="listbox" className={s.list}>
                {options.map((role, i) => (
                  <li key={role.id} role="option" aria-selected={i === highlight}>
                    <button
                      type="button"
                      onMouseDown={(e) => e.preventDefault()}
                      onMouseEnter={() => setHighlight(i)}
                      onClick={() => add(role)}
                      className={cx(s.option, i === highlight && s.optionActive)}
                    >
                      <Dot color={role.color} />
                      <span className={s.name}>{role.name}</span>
                      {role.managed ? <Managed /> : null}
                    </button>
                  </li>
                ))}
              </ul>
            ) : (
              <p className={s.none}>
                {text.trim() ? `No roles match “${text.trim()}”.` : "Every role is already picked."}
              </p>
            )}
          </Floating>
        </div>
      )}
    </div>
  );
}

function RoleChip({
  roleId,
  role,
  loading,
  onRemove,
}: {
  roleId: string;
  role: DirectoryRole | undefined;
  loading: boolean;
  onRemove?: () => void;
}) {
  const deleted = !role && !loading;
  const name = role?.name ?? (loading ? "Loading…" : "Deleted role");
  return (
    <li
      className={s.chip}
      data-deleted={deleted || undefined}
      title={deleted ? `This role (${roleId}) no longer exists in Discord.` : undefined}
    >
      <Dot color={role?.color ?? 0} />
      <span className={s.chipName}>{name}</span>
      {role?.managed ? <Managed /> : null}
      {onRemove ? (
        <button type="button" aria-label={`Remove ${name}`} onClick={onRemove} className={s.remove}>
          <X size={14} />
        </button>
      ) : null}
    </li>
  );
}

function Dot({ color }: { color: number }) {
  const css = roleColor(color);
  return (
    <span
      aria-hidden
      className={s.dot}
      data-plain={css ? undefined : true}
      style={css ? { background: css } : undefined}
    />
  );
}

/**
 * Managed marks a role Discord manages for a bot, a subscription, or Server
 * Boosting: no one can be given it by hand.
 */
function Managed() {
  return (
    <span
      className={s.managed}
      title="Managed by an app or integration. It can't be given by hand."
    >
      Managed
    </span>
  );
}
