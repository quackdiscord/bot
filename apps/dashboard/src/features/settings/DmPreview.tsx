import { Fragment } from "react";

import { Emoji, QuackMessage } from "~/features/discord/QuackMessage";

import type { CaseDm, Segment } from "./caseDm";
import s from "./DmPreview.module.css";

/**
 * DmPreview draws a case DM the way the member sees it in Discord: Quack's
 * icon inline at the start of the lead sentence, the quoted reason, the
 * details, the case reference in subtext, and the appeal button.
 */
export function DmPreview({ dm }: { dm: CaseDm }) {
  return (
    <QuackMessage
      label="Preview of the member's DM"
      buttons={dm.appealable ? ["Appeal decision"] : undefined}
    >
      <div className={s.paragraphs}>
        <p className={s.line}>
          <Emoji name={dm.icon} /> <Segments segments={dm.lead} />
        </p>
        {dm.quote.trim() ? <blockquote className={s.quote}>{dm.quote}</blockquote> : null}
        {dm.details.map((segments, i) => (
          <p key={i} className={s.line}>
            <Segments segments={segments} />
          </p>
        ))}
      </div>
      <p className={s.subtext}>{dm.meta}</p>
    </QuackMessage>
  );
}

function Segments({ segments }: { segments: Segment[] }) {
  return (
    <>
      {segments.map((seg, i) => (
        <Fragment key={i}>
          {seg.strong ? (
            <strong className={s.strong}>{seg.text}</strong>
          ) : seg.time ? (
            <span className={s.timestamp}>{seg.text}</span>
          ) : (
            seg.text
          )}
        </Fragment>
      ))}
    </>
  );
}
