import { createFileRoute } from "@tanstack/react-router";

import { supportUrl } from "~/lib/links";

// quack.bot/support is the short link to the support server.
export const Route = createFileRoute("/support")({
  beforeLoad: () => window.location.replace(supportUrl),
  component: () => null,
});
