import { createFileRoute, Link } from "@tanstack/react-router";
import { ArrowRight, BookOpen } from "lucide-react";

import { CasePreview } from "~/features/site/CasePreview";
import { LadderDemo } from "~/features/site/LadderDemo";
import { SiteFooter, SiteHeader } from "~/features/site/SiteHeader";
import { useTitle } from "~/features/site/useTitle";
import { inviteUrl } from "~/lib/links";
import { ButtonLink, ExternalButton } from "~/ui/Button";
import { QuackIcon, type QuackIconName } from "~/ui/QuackIcon";

import s from "./home.module.css";

export const Route = createFileRoute("/")({
  component: Home,
});

const steps = [
  {
    title: "Write your rules",
    body: "Decide what happens the first time someone breaks a rule, the third time, the fifth. Quack starts you with one you can edit.",
  },
  {
    title: "Add a case",
    body: "A moderator picks the member and the rule they broke, from a slash command, a right-click, or the dashboard.",
  },
  {
    title: "Quack does the rest",
    body: "It checks their history, picks the right punishment, carries it out, DMs the member, and writes it all down.",
  },
];

const features: { icon: QuackIconName; title: string; body: string }[] = [
  {
    icon: "evidence",
    title: "Evidence that sticks",
    body: "Attach screenshots or message links. Quack saves a copy, so it's still there after the message is deleted.",
  },
  {
    icon: "appeal",
    title: "Fair appeals",
    body: "Members can appeal from their DM, even after a ban. Accepting one undoes the punishment for you.",
  },
  {
    icon: "history",
    title: "A record you can trust",
    body: "Every case and decision is logged. Mistakes are voided, not deleted, so nothing quietly disappears.",
  },
  {
    icon: "search",
    title: "A real dashboard",
    body: "Build rules, look up a member's history, and review appeals from your browser.",
  },
  {
    icon: "ticket",
    title: "Support tickets",
    body: "Members open a private thread with your staff. Transcripts are saved when it closes.",
  },
  {
    icon: "shield",
    title: "Spam bot trap",
    body: "A honeypot channel catches bots that post everywhere and handles them with one of your rules.",
  },
];

function Home() {
  useTitle("Quack · Discord moderation that follows your rules");

  return (
    <div className={s.page}>
      <SiteHeader />
      <main>
        <section className={s.hero}>
          <div className={s.heroText}>
            <h1 className={s.title}>
              Discord moderation
              <br />
              <span className={s.highlight}>that follows your rules</span>
            </h1>
            <p className={s.lead}>
              Set up your server's rules once. Your moderators pick the rule someone broke, and
              Quack picks the punishment, carries it out, and keeps the record. Same rule, same
              result, every time.
            </p>
            <div className={s.ctas}>
              <ExternalButton href={inviteUrl} size="lg" className={s.pill}>
                Add to your server
              </ExternalButton>
              <ButtonLink
                to="/docs"
                variant="secondary"
                size="lg"
                className={s.pill}
                icon={<BookOpen size={17} />}
              >
                How it works
              </ButtonLink>
            </div>
          </div>
          <div className={s.heroArt}>
            <CasePreview />
          </div>
        </section>

        <section className={s.section}>
          <div className={s.sectionHead}>
            <h2 className={s.h2}>Three steps, then it runs itself</h2>
            <p className={s.sub}>
              No more arguing in mod chat about whether that's a timeout or a ban.
            </p>
          </div>
          <ol className={s.steps}>
            {steps.map((step, i) => (
              <li key={step.title} className={s.step}>
                <span className={s.stepNumber}>{i + 1}</span>
                <h3 className={s.h3}>{step.title}</h3>
                <p className={s.body}>{step.body}</p>
              </li>
            ))}
          </ol>
        </section>

        <section className={s.split}>
          <div className={s.splitText}>
            <h2 className={s.h2}>Repeat offenses, handled</h2>
            <p className={s.sub}>
              Each rule can get stricter the more times a member breaks it. Quack counts their cases
              for that rule and picks the right step. Your moderators never have to look it up.
            </p>
            <p className={s.sub}>
              Every new server starts with this rule. Change the steps, add more rules, or start
              over. It's your server.
            </p>
            <Link to="/docs/rules" className={s.more}>
              How rules work <ArrowRight size={16} />
            </Link>
          </div>
          <LadderDemo />
        </section>

        <section className={s.section}>
          <div className={s.sectionHead}>
            <h2 className={s.h2}>Everything a mod team needs</h2>
            <p className={s.sub}>And nothing it doesn't.</p>
          </div>
          <ul className={s.features}>
            {features.map((f) => (
              <li key={f.title} className={s.feature}>
                <QuackIcon name={f.icon} size={26} />
                <h3 className={s.h3}>{f.title}</h3>
                <p className={s.body}>{f.body}</p>
              </li>
            ))}
          </ul>
        </section>

        <section className={s.final}>
          <h2 className={s.finalTitle}>Give your mods a break.</h2>
          <p className={s.sub}>Add Quack, check the starter rule, and you're ready.</p>
          <div className={s.ctas}>
            <ExternalButton href={inviteUrl} size="lg" className={s.pill}>
              Add to your server
            </ExternalButton>
            <ButtonLink to="/docs/getting-started" variant="secondary" size="lg" className={s.pill}>
              Setup guide
            </ButtonLink>
          </div>
        </section>
      </main>
      <SiteFooter />
    </div>
  );
}
