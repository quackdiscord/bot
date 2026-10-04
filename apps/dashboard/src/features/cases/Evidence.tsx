import { ExternalLink, FileText } from "lucide-react";

import { useUser } from "~/api/directory";
import type { CaseEvidence } from "~/api/types";
import { bytes, stamp } from "~/lib/format";
import { Avatar } from "~/ui/Avatar";
import { QuackIcon } from "~/ui/QuackIcon";

import s from "./Evidence.module.css";

type Attachment = NonNullable<CaseEvidence["attachments"]>[number];

/**
 * EvidenceMessage renders a captured Discord message the way Discord shows
 * it, from Quack's snapshot, so it still reads right after the original is
 * edited or deleted.
 */
export function EvidenceMessage({
  evidence,
  guildId,
}: {
  evidence: CaseEvidence;
  /** Staff views resolve the author; member views pass nothing. */
  guildId?: string;
}) {
  const { data: author } = useUser(guildId ?? "", guildId ? evidence.author_discord_user_id : null);
  const name = author?.display_name ?? "Message author";
  const failed =
    evidence.capture_outcome !== "captured" && !evidence.content && !evidence.attachments?.length;

  return (
    <article className={s.message}>
      <Avatar src={author?.avatar_url} name={name} size={40} />
      <div className={s.body}>
        <header className={s.meta}>
          <span className={s.author}>{name}</span>
          <time dateTime={evidence.message_created_at} className={s.time}>
            {stamp(evidence.message_created_at)}
          </time>
          {evidence.message_edited_at ? <span className={s.time}>(edited)</span> : null}
          {evidence.message_url ? (
            <a href={evidence.message_url} target="_blank" rel="noreferrer" className={s.jump}>
              Jump <ExternalLink size={12} />
            </a>
          ) : null}
        </header>
        {failed ? (
          <p className={s.muted}>Quack couldn't capture this message.</p>
        ) : evidence.content ? (
          <p className={s.content}>{evidence.content}</p>
        ) : null}
        {evidence.attachments?.length ? (
          <div className={s.attachments}>
            {evidence.attachments.map((a, i) => (
              <AttachmentView key={`${a.filename}-${i}`} attachment={a} />
            ))}
          </div>
        ) : null}
        {evidence.capture_warning ? (
          <p className={s.warning}>
            <QuackIcon name="warn" size={14} /> {evidence.capture_warning}
          </p>
        ) : null}
      </div>
    </article>
  );
}

function AttachmentView({ attachment: a }: { attachment: Attachment }) {
  const url = a.preserved_url || a.original_url;
  const image = a.content_type.startsWith("image/");
  return (
    <div className={s.attachment}>
      {image && url ? (
        <a href={url} target="_blank" rel="noreferrer">
          <img src={url} alt={a.filename} loading="lazy" className={s.image} />
        </a>
      ) : (
        <a href={url} target="_blank" rel="noreferrer" className={s.file}>
          <FileText size={28} />
          <span className={s.fileText}>
            <span className={s.fileName}>{a.filename}</span>
            <span className={s.time}>{bytes(a.size_bytes)}</span>
          </span>
        </a>
      )}
      {a.warning || !a.preserved_url ? (
        <p className={s.warning}>
          <QuackIcon name="warn" size={14} />
          {a.warning || "Quack couldn't keep a copy of this file. The link may stop working."}
        </p>
      ) : null}
    </div>
  );
}
