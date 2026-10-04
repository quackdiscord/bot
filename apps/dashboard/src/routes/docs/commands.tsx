import { createFileRoute } from "@tanstack/react-router";

import { CommandTable, Doc, Section } from "~/features/docs/Doc";

export const Route = createFileRoute("/docs/commands")({
  component: Commands,
});

function Commands() {
  return (
    <Doc
      title="Commands"
      lead="Every Quack command in one place. You'll mostly use /case add; the rest are there when you need them."
    >
      <Section id="cases" title="Cases">
        <CommandTable
          rows={[
            ["/case add", "Add a case for a member under a rule."],
            ["Apps → Add case", "Right-click a message to add a case with it as evidence."],
            ["Apps → Add case for member", "Right-click a member to add a case."],
            ["/case evidence", "Add a file or message link to a case."],
            ["/case view", "Show one case."],
            ["/case list", "Show recent cases."],
            ["/case user", "Show a member's case history."],
            ["/case void", "Cancel a case that was a mistake."],
            ["/case failures", "Show punishments that didn't go through."],
            ["/case retry", "Try a failed punishment again."],
            ["/case dismiss", "Clear a failure from the list. It stays in history."],
            ["/case reverse", "Remove a timeout or ban from a case."],
          ]}
        />
      </Section>

      <Section id="rules" title="Rules">
        <CommandTable
          rows={[
            ["/template create", "Make a rule."],
            ["/template view", "Show a rule and its levels."],
            ["/template edit", "Change a rule's name, reason, appeals, or counting window."],
            ["/template level", "Add or change a level."],
            ["/template remove-level", "Remove a level."],
            ["/template archive", "Stop a rule being used. Its cases are kept."],
            ["/template restore", "Bring back an archived rule."],
          ]}
        />
      </Section>

      <Section id="appeals" title="Appeals">
        <CommandTable rows={[["/appeals", "Show appeals waiting for a decision."]]} />
      </Section>

      <Section id="setup" title="Setup">
        <CommandTable
          rows={[
            ["/setup appeals", "Pick where appeals go, and an invite for unbanned members."],
            ["/setup audit", "Post Quack's moderation activity in a channel."],
            ["/setup tickets", "Let members open private support threads."],
            ["/setup logging", "Log deleted messages and other server changes."],
            ["/setup honeypot", "Set a trap channel for spam bots."],
            ["/help", "A quick guide, right in Discord."],
          ]}
        />
      </Section>
    </Doc>
  );
}
