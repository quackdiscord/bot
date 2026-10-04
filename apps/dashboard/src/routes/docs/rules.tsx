import { createFileRoute, Link } from "@tanstack/react-router";

import { Cmd, Doc, Note, Section, Step, Steps } from "~/features/docs/Doc";

export const Route = createFileRoute("/docs/rules")({
  component: Rules,
});

function Rules() {
  return (
    <Doc
      title="Rules"
      lead="A rule is something members shouldn't do, and what happens when they do it. Rules are how you tell Quack how your server is moderated."
    >
      <Section id="what" title="What's in a rule">
        <ul>
          <li>
            <b>A name</b>, like Spam, Harassment, or Advertising. Name rules after what someone did,
            not the punishment.
          </li>
          <li>
            <b>A reason</b> the member sees in their DM and case. Moderators can't change it, so
            every member gets the same explanation.
          </li>
          <li>
            <b>Levels</b>: what happens on the first case, and what changes if they keep breaking
            the rule.
          </li>
          <li>
            <b>Whether cases can be appealed.</b>
          </li>
          <li>
            <b>Questions for the moderator</b>, if you want them to fill something in, like what the
            member posted or a message link.
          </li>
        </ul>
      </Section>

      <Section id="levels" title="Levels and repeat offenses">
        <p>
          Every rule has a default level that applies to a member's first case. You can add more
          levels that kick in after a certain number of cases. Each level can:
        </p>
        <ul>
          <li>Just record the case (a warning),</li>
          <li>time the member out for a set length of time,</li>
          <li>kick them, or</li>
          <li>ban them, optionally deleting their recent messages.</li>
        </ul>
        <p>
          Quack counts a member's cases <b>for that rule only</b>, in that server only. Three spam
          cases won't make a harassment case stricter. A level keeps applying until the next one
          starts.
        </p>
        <p>You can also choose whether each level sends the member a DM.</p>
        <Note>
          Want old cases to stop counting after a while? Set a <b>counting window</b>, like 30 days.
          Older cases stay in history but no longer make the punishment stricter.
        </Note>
      </Section>

      <Section id="make" title="Make or change a rule">
        <Steps>
          <Step title="Open the dashboard">
            <p>
              Go to the <Link to="/guilds">dashboard</Link>, pick your server, then open{" "}
              <b>Rules</b>.
            </p>
          </Step>
          <Step title="Create a rule or open one">
            <p>Fill in the name and reason, then set up your levels.</p>
          </Step>
          <Step title="Save">
            <p>The rule is ready to use right away. Changes only affect new cases.</p>
          </Step>
        </Steps>
        <p>
          You can also do the basics from Discord with <Cmd>/template create</Cmd>,{" "}
          <Cmd>/template level</Cmd>, and <Cmd>/template edit</Cmd>, but the dashboard is easier.
        </p>
      </Section>

      <Section id="archive" title="Retiring a rule">
        <p>
          Rules are never deleted. <b>Archive</b> a rule to stop it being used for new cases. Its
          old cases stay where they are, and you can restore it any time.
        </p>
        <p>
          Rules can also be exported and imported, if you want to copy your setup to another server.
        </p>
      </Section>
    </Doc>
  );
}
