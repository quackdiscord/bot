import { createFileRoute, Link } from "@tanstack/react-router";

import { Doc, Note, Section, Terms } from "~/features/docs/Doc";

export const Route = createFileRoute("/docs/")({
  component: Intro,
});

function Intro() {
  return (
    <Doc
      title="What is Quack?"
      lead="Quack is a Discord bot that helps your staff moderate the same way every time. You write the rules, your moderators apply them, and Quack handles the punishment and the paperwork."
    >
      <Section id="the-idea" title="The idea">
        <p>
          On most servers, moderators decide punishments on the spot. One mod gives a warning,
          another gives a ban for the same thing, and nobody remembers who already got a second
          chance.
        </p>
        <p>
          Quack fixes that with <b>rules</b>. A rule says what happens when someone breaks it, and
          how it gets stricter if they keep doing it. When a moderator adds a case, they only pick
          the member and the rule. Quack looks at the member's history and does the right thing.
        </p>
      </Section>

      <Section id="words" title="Words you'll see">
        <Terms
          items={[
            {
              term: "Rule",
              meaning:
                "Something members shouldn't do, like spam or harassment, plus what happens when they do it.",
            },
            {
              term: "Level",
              meaning:
                "One step of a rule. For example: warning on the first case, timeout on the third, ban on the fifth.",
            },
            {
              term: "Case",
              meaning:
                "A record that a member broke a rule. It keeps the reason, any evidence, and what Quack did.",
            },
            {
              term: "Appeal",
              meaning: "A member asking staff to take another look at one of their cases.",
            },
            {
              term: "Void",
              meaning:
                "Cancelling a case that was a mistake. It stays in the history but stops counting against the member.",
            },
            {
              term: "Audit log",
              meaning: "The full history of what happened in Quack, and who did it.",
            },
          ]}
        />
      </Section>

      <Section id="where" title="Where you use it">
        <p>
          <b>In Discord</b>, moderators add cases with slash commands or by right-clicking a message
          or member. It's the fastest way to deal with something happening right now.
        </p>
        <p>
          <b>In the dashboard</b>, staff build rules, look up cases, and review appeals. Members
          sign in there too, to see their own cases and appeal them. Both places share the same
          rules and records.
        </p>
        <Note>
          New here? <Link to="/docs/getting-started">Getting started</Link> takes about five
          minutes.
        </Note>
      </Section>
    </Doc>
  );
}
