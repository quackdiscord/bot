import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { Search, X } from "lucide-react";
import { useDeferredValue, useId, useRef, useState } from "react";

import { memberSearchQuery, useUser } from "~/api/directory";
import type { DirectoryUser } from "~/api/types";
import { cx } from "~/lib/cx";
import { isSnowflake } from "~/lib/format";
import { Avatar } from "~/ui/Avatar";
import { TextInput } from "~/ui/Field";
import { Floating } from "~/ui/Floating";
import { Spinner } from "~/ui/Spinner";

import s from "./MemberPicker.module.css";

/**
 * MemberPicker searches the server's current members by name, or takes a
 * pasted user ID, and reports the chosen member's Discord ID.
 */
export function MemberPicker({
  guildId,
  value,
  onChange,
  id,
  autoFocus,
  placeholder = "Search members by name or paste an ID",
}: {
  guildId: string;
  value: string | null;
  onChange: (userId: string | null) => void;
  id?: string;
  autoFocus?: boolean;
  placeholder?: string;
}) {
  const [text, setText] = useState("");
  const [open, setOpen] = useState(false);
  const [highlight, setHighlight] = useState(0);
  const query = useDeferredValue(text.trim());
  const listId = useId();
  const anchor = useRef<HTMLDivElement>(null);

  const search = useQuery({
    ...memberSearchQuery(guildId, query),
    enabled: query.length >= 1 && !isSnowflake(query),
    placeholderData: keepPreviousData,
  });
  const results: DirectoryUser[] = search.data ?? [];

  if (value) return <Selected guildId={guildId} userId={value} onClear={() => onChange(null)} />;

  const pick = (user: DirectoryUser) => {
    onChange(user.id);
    setText("");
    setOpen(false);
  };

  const showId = open && isSnowflake(text);
  const showResults = open && !showId && Boolean(query) && results.length > 0;
  const showNone =
    open && !showId && Boolean(query) && !results.length && !search.isFetching && search.isSuccess;

  return (
    <div ref={anchor} className={s.wrap}>
      <TextInput
        id={id}
        autoFocus={autoFocus}
        value={text}
        placeholder={placeholder}
        autoComplete="off"
        role="combobox"
        aria-expanded={open && results.length > 0}
        aria-controls={listId}
        leading={search.isFetching ? <Spinner size={14} /> : <Search size={16} />}
        onChange={(e) => {
          setText(e.currentTarget.value);
          setOpen(true);
          setHighlight(0);
        }}
        onFocus={() => setOpen(true)}
        onBlur={() => window.setTimeout(() => setOpen(false), 120)}
        onKeyDown={(e) => {
          if (isSnowflake(text) && e.key === "Enter") {
            e.preventDefault();
            onChange(text.trim());
            setText("");
            return;
          }
          if (!results.length) return;
          if (e.key === "ArrowDown") {
            e.preventDefault();
            setHighlight((h) => (h + 1) % results.length);
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            setHighlight((h) => (h - 1 + results.length) % results.length);
          } else if (e.key === "Enter") {
            e.preventDefault();
            const user = results[highlight];
            if (user) pick(user);
          }
        }}
      />
      <Floating anchor={anchor} open={showId || showResults || showNone}>
        {showId ? (
          <button
            type="button"
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => {
              onChange(text.trim());
              setText("");
            }}
            className={cx(s.option, s.optionActive)}
          >
            Use user ID {text.trim()}
          </button>
        ) : showResults ? (
          <ul id={listId} role="listbox" className={s.list}>
            {results.map((user, i) => (
              <li key={user.id} role="option" aria-selected={i === highlight}>
                <button
                  type="button"
                  onMouseDown={(e) => e.preventDefault()}
                  onMouseEnter={() => setHighlight(i)}
                  onClick={() => pick(user)}
                  className={cx(s.option, i === highlight && s.optionActive)}
                >
                  <Avatar src={user.avatar_url} name={user.display_name} size={24} />
                  <span className={s.name}>{user.display_name}</span>
                  <span className={s.handle}>{user.username}</span>
                  {user.bot ? <span className={s.bot}>APP</span> : null}
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <p className={s.none}>No members match “{query}”.</p>
        )}
      </Floating>
    </div>
  );
}

function Selected({
  guildId,
  userId,
  onClear,
}: {
  guildId: string;
  userId: string;
  onClear: () => void;
}) {
  const { data: user } = useUser(guildId, userId);
  const name = user?.display_name ?? userId;
  return (
    <div className={s.selected}>
      <Avatar src={user?.avatar_url} name={name} size={28} />
      <span className={s.name}>{name}</span>
      {user ? <span className={s.handle}>{user.username}</span> : null}
      {user && !user.in_guild ? <span className={s.gone}>Not in server</span> : null}
      <button type="button" aria-label="Clear member" onClick={onClear} className={s.clear}>
        <X size={16} />
      </button>
    </div>
  );
}
