import { createFileRoute } from "@tanstack/react-router";

import { Empty } from "~/ui/States";
import s from "./empty.module.css";

export const Route = createFileRoute("/_authed/guilds/$guildId/appeals/")({
  component: NothingSelected,
});

/** NothingSelected fills the detail pane until an appeal is picked. */
function NothingSelected() {
  return (
    <div className={s.center}>
      <Empty icon="appeal" title="Pick an appeal to review">
        Read the member's side, check the case, and decide. Use <kbd className={s.kbd}>J</kbd> and{" "}
        <kbd className={s.kbd}>K</kbd> to move through the queue.
      </Empty>
    </div>
  );
}
