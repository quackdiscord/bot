import { createFileRoute } from "@tanstack/react-router";

import { inviteUrl } from "~/lib/links";

// quack.bot/invite is the short link people share to add Quack.
export const Route = createFileRoute("/invite")({
  beforeLoad: () => window.location.replace(inviteUrl),
  component: () => null,
});
