const files = import.meta.glob<string>("../../../../assets/icons/quack/svg/*.svg", {
  query: "?url",
  import: "default",
  eager: true,
});

const urls: Record<string, string> = {};
for (const [path, url] of Object.entries(files)) {
  const name = path.slice(path.lastIndexOf("/quack_") + 7, -4);
  urls[name] = url;
}

/**
 * QuackIconName names an icon from the Quack Signals set, the same icons
 * Quack's Discord messages use, so the dashboard and the bot speak one
 * visual language.
 */
export type QuackIconName =
  | "accept"
  | "appeal"
  | "ban"
  | "calendar"
  | "case_add"
  | "case"
  | "case_void"
  | "decline"
  | "delete"
  | "duck"
  | "edit"
  | "error"
  | "evidence"
  | "history"
  | "info"
  | "join"
  | "kick"
  | "leave"
  | "link"
  | "lock"
  | "member"
  | "message"
  | "note"
  | "pending"
  | "pin"
  | "reply"
  | "retry"
  | "review"
  | "running"
  | "search"
  | "settings"
  | "shield"
  | "spark"
  | "success"
  | "ticket"
  | "timeout"
  | "unban"
  | "unlock"
  | "untimeout"
  | "warn";

/** QuackIcon draws one Quack Signals icon in its own color. */
export function QuackIcon({
  name,
  size = 20,
  label,
  className,
}: {
  name: QuackIconName;
  size?: number;
  label?: string;
  className?: string;
}) {
  return (
    <img
      src={urls[name]}
      width={size}
      height={size}
      alt={label ?? ""}
      aria-hidden={label ? undefined : true}
      draggable={false}
      className={className}
      style={{ flexShrink: 0, userSelect: "none" }}
    />
  );
}
