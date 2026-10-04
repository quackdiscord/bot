import { useState } from "react";

import { cx } from "~/lib/cx";

import s from "./Avatar.module.css";

/**
 * Avatar is a round user picture, or a guild icon when square is set.
 * Missing or broken images fall back to initials on a stable color.
 */
export function Avatar({
  src,
  name,
  size = 32,
  square,
}: {
  src?: string | null;
  name: string;
  size?: number;
  square?: boolean;
}) {
  const [broken, setBroken] = useState(false);
  const shape = square ? s.square : s.round;
  if (src && !broken) {
    return (
      <img
        src={sized(src, size)}
        alt=""
        width={size}
        height={size}
        loading="lazy"
        decoding="async"
        onError={() => setBroken(true)}
        className={cx(s.avatar, shape)}
      />
    );
  }
  return (
    <span
      aria-hidden
      className={cx(s.avatar, shape, s.fallback)}
      style={{
        width: size,
        height: size,
        fontSize: Math.max(10, Math.round(size * 0.36)),
        background: `hsl(${hue(name)} 38% 36%)`,
      }}
    >
      {initials(name)}
    </span>
  );
}

/** sized asks Discord's CDN for an image close to the rendered size. */
function sized(src: string, size: number): string {
  if (!src.startsWith("https://cdn.discordapp.com/") || src.includes("/embed/")) return src;
  const want = [16, 32, 64, 128, 256, 512].find((n) => n >= size * 2) ?? 512;
  const url = new URL(src);
  url.searchParams.set("size", String(want));
  return url.toString();
}

function initials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return "?";
  if (words.length === 1) return words[0]!.slice(0, 2);
  return words
    .slice(0, 3)
    .map((w) => w[0])
    .join("");
}

function hue(name: string): number {
  let h = 0;
  for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) % 360;
  return h;
}
