import type { AppealStatus } from "~/api/types";

/** Decision is one staff move on an appeal, named after its API route. */
export type Decision = "accept" | "reject" | "request_information" | "close" | "reopen";

/**
 * decisionsFrom mirrors the backend's appeal transitions: what staff can do
 * from each status. Accepting is final; rejected and closed appeals can be
 * reopened, which asks the member for more information.
 */
export const decisionsFrom: Record<AppealStatus, Decision[]> = {
  pending: ["accept", "reject", "request_information", "close"],
  needs_information: ["close"],
  accepted: [],
  rejected: ["reopen"],
  closed: ["reopen"],
};

/**
 * Discord sends these when a server doesn't require a decision reason, so
 * the member reads the same notice whichever surface staff used.
 */
const defaultReasons: Partial<Record<Decision, string>> = {
  accept: "This case has been voided.",
  reject: "Appeal rejected.",
  close: "Appeal closed.",
};

/**
 * reasonRequired says whether staff must write the text for a decision.
 * Requests for information and reopens are a message to the member, so they
 * always need one. Decisions follow the server's setting, which staff appeal
 * responses carry as review_reason_required (omitted when off).
 */
export function reasonRequired(decision: Decision, guildRequires: boolean | undefined): boolean {
  if (!(decision in defaultReasons)) return true;
  return guildRequires === true;
}

/** decisionReason is what to send: the staff text, or the standard notice. */
export function decisionReason(decision: Decision, text: string): string {
  return text.trim() || defaultReasons[decision] || "";
}

/** The status filter in the appeal queue's URL. "all" lists every status. */
export type StatusFilter = AppealStatus | "all";

/** FilterTab is a top-level tab; decided appeals share one, split below it. */
export type FilterTab = "pending" | "needs_information" | "decided" | "all";

const decided: AppealStatus[] = ["accepted", "rejected", "closed"];

/** tabOf finds the tab a status filter belongs to. */
export function tabOf(filter: StatusFilter): FilterTab {
  return decided.includes(filter as AppealStatus) ? "decided" : (filter as FilterTab);
}

/** filterOf picks the status filter a tab opens with. */
export function filterOf(tab: FilterTab): StatusFilter {
  return tab === "decided" ? "accepted" : tab;
}

/** parseStatusFilter reads the filter from search params, defaulting to pending. */
export function parseStatusFilter(value: unknown): StatusFilter {
  const all: StatusFilter[] = ["pending", "needs_information", ...decided, "all"];
  return all.includes(value as StatusFilter) ? (value as StatusFilter) : "pending";
}
