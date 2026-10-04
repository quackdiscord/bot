import { createFileRoute } from "@tanstack/react-router";

import { Cmd, Doc, Note, Section, Step, Steps } from "~/features/docs/Doc";

export const Route = createFileRoute("/docs/appeals")({
  component: Appeals,
});

function Appeals() {
  return (
    <Doc
      title="Appeals"
      lead="Members can ask your staff to take another look at a case. Here's how it works on both sides."
    >
      <Section id="members" title="For members">
        <Steps>
          <Step title="Open the DM from Quack">
            <p>
              When you get a case, Quack sends you a DM. If the case can be appealed, it has an{" "}
              <b>Appeal decision</b> button. This works even if you've been banned.
            </p>
          </Step>
          <Step title="Sign in with Discord">
            <p>The button opens this website. Sign in so Quack knows it's you.</p>
          </Step>
          <Step title="Explain why">
            <p>
              Tell the staff why the case should be looked at again. Keep it honest and specific.
            </p>
          </Step>
          <Step title="Wait for a reply">
            <p>You'll get the decision by DM. You can also check on it in the same place.</p>
          </Step>
        </Steps>
        <Note>Each case can be appealed once, so take your time with it.</Note>
      </Section>

      <Section id="staff" title="For staff">
        <p>
          Run <Cmd>/setup appeals</Cmd> to pick the channel where new appeals show up. Review them
          there, with <Cmd>/appeals</Cmd>, or on the <b>Appeals</b> page in the dashboard.
        </p>
        <ul>
          <li>
            <b>Accept</b> voids the case and removes its timeout or ban.
          </li>
          <li>
            <b>Reject</b> keeps the case as it is.
          </li>
          <li>You can also ask the member for more information before you decide.</li>
        </ul>
        <p>Either way, the member gets the decision by DM.</p>
        <Note>
          Add a server invite with the <Cmd>rejoin</Cmd> option on <Cmd>/setup appeals</Cmd>, so
          members who get unbanned have a way back in.
        </Note>
      </Section>

      <Section id="turn-off" title="Turning appeals off">
        <p>
          Appeals are set per rule. Turn them off for a rule in the dashboard, or with{" "}
          <Cmd>/template edit</Cmd>.
        </p>
      </Section>
    </Doc>
  );
}
