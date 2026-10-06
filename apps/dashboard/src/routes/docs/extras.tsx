import { createFileRoute } from "@tanstack/react-router";

import { Cmd, Doc, Note, Section } from "~/features/docs/Doc";

export const Route = createFileRoute("/docs/extras")({
  component: Extras,
});

function Extras() {
  return (
    <Doc
      title="Tickets, logs & more"
      lead="Optional features you can turn on with one command each. None of them are needed to use Quack."
    >
      <Note>
        Run any setup command without picking channels and Quack creates what it needs. Or choose
        channels you already have.
      </Note>

      <Section id="tickets" title="Tickets">
        <p>
          <Cmd>/setup tickets</Cmd> posts a panel where members can open a private thread with your
          staff. When the ticket is closed, Quack saves a transcript and DMs the member a copy when
          it can.
        </p>
      </Section>

      <Section id="audit" title="Moderation log">
        <p>
          <Cmd>/setup audit</Cmd> posts Quack's activity in a staff channel: new cases, voids,
          appeal decisions, rule changes, and so on. The full history is always in the dashboard
          too.
        </p>
      </Section>

      <Section id="evidence" title="Evidence copies">
        <p>
          <Cmd>/setup evidence</Cmd> picks a staff-only channel where Quack keeps copies of evidence
          files, so they stay viewable after the original message is deleted. Without it, Quack
          keeps each file's details and a link to the original.
        </p>
      </Section>

      <Section id="logging" title="Server logs">
        <p>
          <Cmd>/setup logging</Cmd> keeps an eye on your server and posts things like edited and
          deleted messages, and changes to channels and the server, in a staff channel.
        </p>
      </Section>

      <Section id="honeypot" title="Honeypot">
        <p>
          <Cmd>/setup honeypot</Cmd> creates a trap channel with a warning not to post in it. Real
          members read the warning. Spam bots don't. Anyone who posts there gets a case under the
          rule you pick, and moderators are left alone.
        </p>
        <Note tone="warn">
          Check which rule the honeypot uses before turning it on. Whatever that rule does, it does
          automatically.
        </Note>
      </Section>

      <Section id="pause" title="Pausing a feature">
        <p>
          Use <Cmd>enabled:false</Cmd> on its own with <Cmd>/setup tickets</Cmd>,{" "}
          <Cmd>/setup logging</Cmd>, or <Cmd>/setup honeypot</Cmd> to pause it. Use{" "}
          <Cmd>enabled:true</Cmd> to turn it back on. Your channels are kept. You can also manage
          all of these in the dashboard under <b>Settings</b>.
        </p>
      </Section>
    </Doc>
  );
}
