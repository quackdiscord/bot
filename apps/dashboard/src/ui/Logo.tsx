/** Logo is Quack's duck-face mark, the same one as the bot's avatar. */
export function Logo({ size = 32, label }: { size?: number; label?: string }) {
  return (
    <img
      src="/logo.webp"
      width={size}
      height={size}
      alt={label ?? ""}
      aria-hidden={label ? undefined : true}
      draggable={false}
      style={{ flexShrink: 0, borderRadius: "50%", userSelect: "none" }}
    />
  );
}
