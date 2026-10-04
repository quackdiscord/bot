import { Check } from "lucide-react";
import { Fragment, type ReactNode } from "react";

import { cx } from "~/lib/cx";
import { stamp } from "~/lib/format";
import { QuackIcon, type QuackIconName } from "~/ui/QuackIcon";

import { splitIcons } from "./icons";
import s from "./QuackMessage.module.css";

/**
 * QuackMessage frames content as a Discord message from the Quack app: the
 * duck avatar, the name with its APP tag, and a timestamp. Previews use it
 * so admins see what members will see, so it keeps Discord's own look
 * rather than the dashboard's.
 */
export function QuackMessage({
  label,
  children,
  buttons,
}: {
  /** Names the preview for screen readers. */
  label: string;
  children: ReactNode;
  /** Button labels shown under the message, in Discord's blurple. */
  buttons?: string[];
}) {
  return (
    <div role="figure" aria-label={label} className={s.channel}>
      <div className={s.message}>
        <span className={s.avatar} aria-hidden>
          <QuackIcon name="duck" size={26} />
        </span>
        <div className={s.content}>
          <p className={s.header}>
            <span className={s.author}>Quack</span>
            <span className={s.app}>
              <Check size={10} strokeWidth={4} aria-hidden />
              APP
            </span>
            <time className={s.time}>{stamp(new Date().toISOString())}</time>
          </p>
          <div className={s.body}>{children}</div>
          {buttons?.length ? (
            <div className={s.components}>
              {buttons.map((b) => (
                <span key={b} className={s.button}>
                  {b}
                </span>
              ))}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

/**
 * Emoji is a Signals icon inline in message text, sized and aligned like a
 * Discord custom emoji so it sits on the same line as the words after it.
 */
export function Emoji({ name }: { name: QuackIconName }) {
  return <QuackIcon name={name} size={22} className={s.emoji} />;
}

/** Inline renders text with its {{quack:key}} icons as emoji. */
export function Inline({ text }: { text: string }) {
  return (
    <>
      {splitIcons(text).map((piece, i) => (
        <Fragment key={i}>{"icon" in piece ? <Emoji name={piece.icon} /> : piece.text}</Fragment>
      ))}
    </>
  );
}

/**
 * MessageMarkdown renders the little Markdown Quack's posts use: headings
 * ("# "), subtext ("-# "), icons, and plain lines. Everything else shows as
 * typed.
 */
export function MessageMarkdown({ text }: { text: string }) {
  return (
    <>
      {text.split("\n").map((line, i) => {
        const heading = /^(#{1,3}) (.*)$/.exec(line);
        if (heading) {
          const level = (["h1", "h2", "h3"] as const)[heading[1]!.length - 1]!;
          return (
            <p key={i} className={cx(s.heading, s[level])}>
              <Inline text={heading[2]!} />
            </p>
          );
        }
        if (line.startsWith("-# ")) {
          return (
            <p key={i} className={s.subtext}>
              <Inline text={line.slice(3)} />
            </p>
          );
        }
        return (
          <p key={i} className={s.line}>
            {line ? <Inline text={line} /> : " "}
          </p>
        );
      })}
    </>
  );
}
