import { createFileRoute, Link } from "@tanstack/react-router";

import { Cmd, Doc, Question } from "~/features/docs/Doc";
import { supportUrl } from "~/lib/links";

export const Route = createFileRoute("/docs/faq")({
  component: Faq,
});

function Faq() {
  return (
    <Doc title="Common questions" lead="Quick answers to the things people ask most.">
      <Question q="Where are /warn, /timeout, and /ban?">
        <p>
          They're gone. Instead of picking a punishment, you pick the rule someone broke with{" "}
          <Cmd>/case add</Cmd>, and Quack picks the punishment from that rule. A warning is just a
          case on a level with no timeout, kick, or ban.
        </p>
      </Question>

      <Question q="Quack says it can't time out or ban someone">
        <p>
          Quack's role needs to be higher than the member's highest role. Go to{" "}
          <b>Server Settings → Roles</b> and drag Quack up. Then retry it from{" "}
          <Cmd>/case failures</Cmd>.
        </p>
      </Question>

      <Question q="Can I choose a harsher punishment this one time?">
        <p>
          No, and that's on purpose: the same rule gives the same result for everyone. If something
          deserves a different response, make a separate rule for it, like "Severe harassment" with
          a ban as its first level.
        </p>
      </Question>

      <Question q="I added a case by mistake">
        <p>
          Void it with <Cmd>/case void</Cmd> or from the case in the dashboard. It stays in the
          history but stops counting, and any timeout or ban it gave is removed when possible. See{" "}
          <Link to="/docs/cases" hash="mistakes">
            Fixing a mistake
          </Link>
          .
        </p>
      </Question>

      <Question q="If I change a rule, do old cases change?">
        <p>
          No. Every case remembers the rule exactly as it was when the case was made. Changes only
          affect new cases. Old cases still count toward the new levels, though.
        </p>
      </Question>

      <Question q="The member didn't get a DM">
        <p>
          Some people block DMs from servers. The case still counts, and the member can always see
          it by signing in to this website.
        </p>
      </Question>

      <Question q="Does a member's history carry over between servers?">
        <p>No. Cases only count in the server they were made in.</p>
      </Question>

      <Question q="I got banned. How do I appeal?">
        <p>
          Open the DM Quack sent you and press <b>Appeal decision</b>. If you can't find it, sign in
          to the <Link to="/guilds">dashboard</Link> to see your cases. More in{" "}
          <Link to="/docs/appeals">Appeals</Link>.
        </p>
      </Question>

      <Question q="Something else is wrong">
        <p>
          Ask in the <a href={supportUrl}>support server</a> and we'll help you out.
        </p>
      </Question>
    </Doc>
  );
}
