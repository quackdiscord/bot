import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";

import { ApiError, api, unwrap } from "~/api/client";
import { authQuery, keys, memberAppealQuery } from "~/api/queries";
import type { Appeal, AppealEvent, MemberCase } from "~/api/types";
import { cx } from "~/lib/cx";
import { appealMeta, fullDate, stamp } from "~/lib/format";
import { Avatar } from "~/ui/Avatar";
import { Badge } from "~/ui/Badge";
import { Button } from "~/ui/Button";
import { Field, TextArea } from "~/ui/Field";
import { Heading, Panel } from "~/ui/Page";
import { QuackIcon, type QuackIconName } from "~/ui/QuackIcon";
import { ErrorState, SkeletonRows } from "~/ui/States";

import s from "./Appeal.module.css";

/** APPEAL_LIMIT matches the backend's statement and reply limit. */
const APPEAL_LIMIT = 4000;

/** The one question every appeal asks. Guilds can't change it. */
const QUESTION = "Why should staff reconsider this case?";

/**
 * AppealSection is the member's side of an appeal: the form when they can
 * still appeal, otherwise the appeal's status and conversation, with a reply
 * box while staff are waiting on them. Staff are never named.
 */
export function AppealSection({ memberCase: c }: { memberCase: MemberCase }) {
  const [sent, setSent] = useState(false);

  if (c.appeal_id) {
    return (
      <section className={s.block}>
        <Heading>Your appeal</Heading>
        {sent ? (
          <Callout icon="success" title="Appeal sent" tone="success">
            Staff will review it here. Quack will DM you when they decide, if your DMs are open.
          </Callout>
        ) : null}
        <AppealThread appealId={c.appeal_id} memberCase={c} />
      </section>
    );
  }

  if (!c.appealable) {
    return (
      <section className={s.block}>
        <Heading>Appeal</Heading>
        <Callout icon="lock" title="This case can't be appealed">
          {c.validity === "voided"
            ? "It was voided, so there's nothing left to appeal."
            : "This server's rule doesn't allow appeals for it."}
        </Callout>
      </section>
    );
  }

  return (
    <section className={s.block}>
      <Heading>Appeal</Heading>
      <AppealForm caseId={c.id ?? ""} onSent={() => setSent(true)} />
    </section>
  );
}

function AppealForm({ caseId, onSent }: { caseId: string; onSent: () => void }) {
  const queryClient = useQueryClient();
  const [statement, setStatement] = useState("");
  const [error, setError] = useState<string | null>(null);

  const submit = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST("/members/me/cases/{caseID}/appeal", {
          params: { path: { caseID: caseId } },
          body: { statement: statement.trim() },
        }),
      ),
    onSuccess: ({ appeal }) => {
      queryClient.setQueryData(memberAppealQuery(appeal.id).queryKey, appeal);
      void queryClient.invalidateQueries({ queryKey: keys.member });
      onSent();
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Couldn't send your appeal."),
  });

  const length = statement.trim().length;
  return (
    <Panel>
      <form
        className={s.form}
        onSubmit={(e) => {
          e.preventDefault();
          setError(null);
          if (length > 0 && length <= APPEAL_LIMIT) submit.mutate();
        }}
      >
        <p className={s.intro}>
          You get one appeal per case, so include everything staff should know. They'll see your
          answer along with the case.
        </p>
        <Field label={QUESTION} required error={error}>
          {(id) => (
            <TextArea
              id={id}
              rows={7}
              maxLength={APPEAL_LIMIT}
              value={statement}
              invalid={Boolean(error)}
              placeholder="Explain what happened in your own words."
              onChange={(e) => setStatement(e.currentTarget.value)}
            />
          )}
        </Field>
        <div className={s.formFooter}>
          <Counter length={statement.length} />
          <Button type="submit" disabled={length === 0} pending={submit.isPending}>
            Send appeal
          </Button>
        </div>
      </form>
    </Panel>
  );
}

function AppealThread({ appealId, memberCase: c }: { appealId: string; memberCase: MemberCase }) {
  const appeal = useQuery(memberAppealQuery(appealId));
  if (appeal.isPending) return <SkeletonRows rows={3} />;
  if (appeal.isError) {
    return <ErrorState error={appeal.error} retry={() => void appeal.refetch()} />;
  }
  const a = appeal.data;
  const meta = appealMeta[a.status];
  return (
    <>
      <Panel className={s.status}>
        <QuackIcon name={meta.icon} size={28} />
        <div className={s.statusText}>
          <div className={s.statusTitle}>
            <Badge tone={meta.tone} dot>
              {meta.label}
            </Badge>
          </div>
          <p className={s.statusBody}>{statusCopy(a)}</p>
        </div>
      </Panel>
      <Panel>
        <ol className={s.thread}>
          <Message
            author="you"
            created={a.created_at}
            title="You appealed"
            body={a.statement}
            memberCase={c}
          />
          {(a.events ?? [])
            .filter((e) => e.type !== "submitted")
            .sort((x, y) => x.created_at.localeCompare(y.created_at))
            .map((e) => (
              <Message
                key={e.id}
                author={e.actor_type === "member" ? "you" : "staff"}
                created={e.created_at}
                title={eventTitle(e)}
                body={e.body}
                memberCase={c}
              />
            ))}
        </ol>
      </Panel>
      {a.status === "needs_information" ? <Reply appealId={a.id} /> : null}
    </>
  );
}

function statusCopy(a: Appeal): string {
  switch (a.status) {
    case "pending":
      return "Staff haven't decided yet. You'll see their answer here.";
    case "needs_information":
      return "Staff need more from you before they decide. Reply below.";
    case "accepted":
      return "Staff accepted your appeal and voided the case, so it no longer counts against you.";
    case "rejected":
      return "Staff reviewed your appeal and kept the case.";
    case "closed":
      return "Staff closed this appeal without a decision.";
  }
}

const eventTitles: Record<AppealEvent["type"], string> = {
  submitted: "You appealed",
  information_requested: "Staff asked for more information",
  information_submitted: "You replied",
  reopened: "Staff reopened your appeal",
  accepted: "Staff accepted your appeal",
  rejected: "Staff rejected your appeal",
  closed: "Staff closed your appeal",
};

function eventTitle(e: AppealEvent): string {
  return eventTitles[e.type] ?? (e.actor_type === "member" ? "You" : "Staff");
}

/**
 * Message is one turn in the appeal, in Discord's message layout. Staff
 * turns show the server, never the person.
 */
function Message({
  author,
  created,
  title,
  body,
  memberCase: c,
}: {
  author: "you" | "staff";
  created: string;
  title: string;
  body: string;
  memberCase: MemberCase;
}) {
  const { data: me } = useQuery(authQuery);
  const you = me ? me.user.global_name || me.user.username : "You";
  const guild = c.guild_name || "Server";
  return (
    <li className={cx(s.message, author === "staff" && s.staff)}>
      {author === "you" ? (
        <Avatar src={me?.user.avatar_url} name={you} size={40} />
      ) : (
        <Avatar src={c.guild_icon_url} name={guild} size={40} square />
      )}
      <div className={s.messageBody}>
        <header className={s.messageMeta}>
          <span className={s.author}>{author === "you" ? you : `${guild} staff`}</span>
          <time dateTime={created} title={fullDate(created)} className={s.time}>
            {stamp(created)}
          </time>
        </header>
        <p className={s.event}>{title}</p>
        {body ? <p className={s.text}>{body}</p> : null}
      </div>
    </li>
  );
}

function Reply({ appealId }: { appealId: string }) {
  const queryClient = useQueryClient();
  const [body, setBody] = useState("");
  const [error, setError] = useState<string | null>(null);

  const send = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST("/members/me/appeals/{appealID}/information", {
          params: { path: { appealID: appealId } },
          body: { body: body.trim() },
        }),
      ),
    onSuccess: ({ appeal }) => {
      queryClient.setQueryData(memberAppealQuery(appealId).queryKey, appeal);
      void queryClient.invalidateQueries({ queryKey: keys.member });
      setBody("");
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Couldn't send your reply."),
  });

  const length = body.trim().length;
  return (
    <Panel>
      <form
        className={s.form}
        onSubmit={(e) => {
          e.preventDefault();
          setError(null);
          if (length > 0 && length <= APPEAL_LIMIT) send.mutate();
        }}
      >
        <Field label="Your reply" required error={error}>
          {(id) => (
            <TextArea
              id={id}
              rows={5}
              maxLength={APPEAL_LIMIT}
              value={body}
              invalid={Boolean(error)}
              placeholder="Answer what staff asked."
              onChange={(e) => setBody(e.currentTarget.value)}
            />
          )}
        </Field>
        <div className={s.formFooter}>
          <Counter length={body.length} />
          <Button type="submit" disabled={length === 0} pending={send.isPending}>
            Send reply
          </Button>
        </div>
      </form>
    </Panel>
  );
}

function Counter({ length }: { length: number }) {
  return (
    <span
      aria-live="polite"
      className={s.counter}
      data-near={length > APPEAL_LIMIT * 0.9 || undefined}
    >
      {length.toLocaleString()} / {APPEAL_LIMIT.toLocaleString()}
    </span>
  );
}

/** Callout is a short boxed note with an icon, for states with no list. */
export function Callout({
  icon,
  title,
  tone,
  children,
}: {
  icon: QuackIconName;
  title: string;
  tone?: "success";
  children: ReactNode;
}) {
  return (
    <div className={cx(s.callout, tone === "success" && s.success)}>
      <QuackIcon name={icon} size={24} />
      <div className={s.calloutText}>
        <p className={s.calloutTitle}>{title}</p>
        <p className={s.calloutBody}>{children}</p>
      </div>
    </div>
  );
}
