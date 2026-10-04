import { createFileRoute, Link } from "@tanstack/react-router";

import { Cmd, Doc, Note, Section, Step, Steps } from "~/features/docs/Doc";
import { inviteUrl } from "~/lib/links";

export const Route = createFileRoute("/docs/getting-started")({
  component: GettingStarted,
});

function GettingStarted() {
  return (
    <Doc
      title="Getting started"
      lead="Add Quack, give it a moment of setup, and you're ready to moderate."
    >
      <Section id="setup" title="Set up your server">
        <Steps>
          <Step title="Add Quack to your server">
            <p>
              <a href={inviteUrl}>Invite Quack</a> and pick your server. You need the{" "}
              <b>Manage Server</b> permission to add bots.
            </p>
          </Step>
          <Step title="Move Quack's role up">
            <p>
              In <b>Server Settings → Roles</b>, drag the Quack role above the roles of anyone it
              should be able to time out, kick, or ban. Discord won't let a bot act on someone with
              a higher role.
            </p>
          </Step>
          <Step title="Check the starter rule">
            <p>
              Quack comes with one rule, <b>General rule violation</b>: a warning for the first two
              cases, a 24-hour timeout for the third and fourth, and a ban from the fifth on.
            </p>
            <p>
              Open the <Link to="/guilds">dashboard</Link>, pick your server, and go to <b>Rules</b>{" "}
              to change it or add your own. See <Link to="/docs/rules">Rules</Link> for how.
            </p>
          </Step>
          <Step title="Pick an appeals channel">
            <p>
              Run <Cmd>/setup appeals</Cmd> in your server. Quack creates a staff-only channel where
              appeals show up, or you can choose one you already have.
            </p>
          </Step>
          <Step title="Try it out">
            <p>
              Run <Cmd>/case add</Cmd>, pick a rule, and pick a member. Quack posts the result and
              DMs the member. Use a friend or an alt account: Quack won't add cases for you, bots,
              or the server owner.
            </p>
          </Step>
        </Steps>
      </Section>

      <Section id="next" title="Optional extras">
        <p>When you're ready, these each take one command:</p>
        <ul>
          <li>
            <Cmd>/setup audit</Cmd> posts Quack's moderation activity in a staff channel.
          </li>
          <li>
            <Cmd>/setup tickets</Cmd> lets members open private support threads.
          </li>
          <li>
            <Cmd>/setup logging</Cmd> logs deleted and edited messages and other server changes.
          </li>
          <li>
            <Cmd>/setup honeypot</Cmd> sets a trap for spam bots.
          </li>
        </ul>
        <p>
          More on each in <Link to="/docs/extras">Tickets, logs & more</Link>.
        </p>
        <Note>
          Stuck? Run <Cmd>/help</Cmd> in your server for a quick overview, with a link to the right
          dashboard page.
        </Note>
      </Section>
    </Doc>
  );
}
