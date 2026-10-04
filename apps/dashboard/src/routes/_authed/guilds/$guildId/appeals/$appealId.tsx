import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { ChevronLeft } from "lucide-react";

import { appealQuery } from "~/api/queries";
import { CaseSummary } from "~/features/appeals/CaseSummary";
import { Conversation } from "~/features/appeals/Conversation";
import { DecisionBar, ReversalOffers } from "~/features/appeals/Review";
import { UserChip } from "~/features/people/User";
import { appealMeta } from "~/lib/format";
import { Badge } from "~/ui/Badge";
import { QuackIcon } from "~/ui/QuackIcon";
import { ErrorState, SkeletonRows } from "~/ui/States";
import s from "./appeal.module.css";

export const Route = createFileRoute("/_authed/guilds/$guildId/appeals/$appealId")({
  loader: ({ context, params }) =>
    context.queryClient.ensureQueryData(appealQuery(params.guildId, params.appealId)),
  component: AppealPage,
  pendingComponent: () => <SkeletonRows rows={5} />,
  errorComponent: ({ error, reset }) => (
    <div className={s.center}>
      <ErrorState error={error} retry={reset} />
    </div>
  ),
});

function AppealPage() {
  const { guildId, appealId } = Route.useParams();
  const search = Route.useSearch();
  const { data: appeal } = useSuspenseQuery(appealQuery(guildId, appealId));
  const meta = appealMeta[appeal.status];

  return (
    <>
      <header className={s.header}>
        <Link
          to="/guilds/$guildId/appeals"
          params={{ guildId }}
          search={search}
          aria-label="Back to appeals"
          className={s.back}
        >
          <ChevronLeft size={20} />
        </Link>
        <UserChip guildId={guildId} userId={appeal.target_discord_user_id} size={24} />
        <Badge tone={meta.tone} icon={<QuackIcon name={meta.icon} size={14} />}>
          {meta.label}
        </Badge>
      </header>
      <div className={s.scroll}>
        <div className={s.inner} key={appeal.id}>
          <CaseSummary guildId={guildId} appeal={appeal} />
          <Conversation guildId={guildId} appeal={appeal} />
          <ReversalOffers guildId={guildId} appeal={appeal} />
        </div>
      </div>
      <DecisionBar guildId={guildId} appeal={appeal} />
    </>
  );
}
