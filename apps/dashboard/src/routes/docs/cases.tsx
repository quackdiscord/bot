import { createFileRoute, Link } from "@tanstack/react-router";

import { Cmd, Doc, Note, Section } from "~/features/docs/Doc";

export const Route = createFileRoute("/docs/cases")({
  component: Cases,
});

function Cases() {
  return (
    <Doc
      title="Cases"
      lead="A case is the record of a member breaking a rule. Adding one is how you moderate with Quack."
    >
      <Section id="add" title="Add a case">
        <p>There are three ways, and they all do the same thing:</p>
        <ul>
          <li>
            Run <Cmd>/case add</Cmd>, then pick the rule and the member.
          </li>
          <li>
            Right-click a message and choose <Cmd>Apps → Add case</Cmd>. The message is saved as
            evidence.
          </li>
          <li>
            Right-click a member and choose <Cmd>Apps → Add case for member</Cmd>.
          </li>
        </ul>
        <p>
          You can also add cases from the dashboard, on the <b>Cases</b> page.
        </p>
      </Section>

      <Section id="what-happens" title="What happens next">
        <p>Quack takes it from there:</p>
        <ul>
          <li>Counts the member's earlier cases for that rule and picks the right level.</li>
          <li>Times out, kicks, or bans the member if that level says to.</li>
          <li>Sends the member one DM with the reason and what happened.</li>
          <li>Posts a short summary where you ran the command.</li>
        </ul>
        <Note>
          Quack won't add a case if you don't have the Discord permission for the punishment it
          picked. For example, you need <b>Ban Members</b> if the case would ban someone. See{" "}
          <Link to="/docs/permissions">Who can do what</Link>.
        </Note>
      </Section>

      <Section id="evidence" title="Evidence">
        <p>
          Attach a screenshot or paste a message link when you add the case, or add it later with{" "}
          <Cmd>/case evidence</Cmd>. You can link anyone's message, including Quack's own logs; the
          member sees the message but not who wrote it. Quack saves a copy of the message. With{" "}
          <Cmd>/setup evidence</Cmd>, it also keeps copies of the files, so they're still there if
          the original gets deleted.
        </p>
        <Note tone="warn">
          Quack can't save a message that's already gone. Add the case before deleting the message.
        </Note>
      </Section>

      <Section id="mistakes" title="Fixing a mistake">
        <p>
          Cases can't be edited or deleted. If a case was wrong, <b>void</b> it with{" "}
          <Cmd>/case void</Cmd> or the button on the case. A voided case:
        </p>
        <ul>
          <li>stays in the history, marked as voided,</li>
          <li>no longer counts toward the member's punishments, and</li>
          <li>has its timeout or ban removed when possible.</li>
        </ul>
        <p>Then add a new case with the right rule if you need to.</p>
      </Section>

      <Section id="look-up" title="Looking things up">
        <ul>
          <li>
            <Cmd>/case list</Cmd> shows recent cases.
          </li>
          <li>
            <Cmd>/case user</Cmd> shows one member's history.
          </li>
          <li>
            <Cmd>/case view</Cmd> shows one case in full.
          </li>
          <li>
            <Cmd>/case failures</Cmd> shows punishments that didn't go through, usually because
            Quack's role was too low. You can retry or dismiss them.
          </li>
        </ul>
        <p>The dashboard has all of this too, with search.</p>
      </Section>
    </Doc>
  );
}
