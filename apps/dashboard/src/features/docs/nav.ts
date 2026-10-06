/**
 * docGroups is the docs sidebar, in reading order. Previous and next links
 * follow the same order, so a new page only needs adding here.
 */
export const docGroups = [
  {
    title: "Basics",
    pages: [
      { to: "/docs", title: "What is Quack?" },
      { to: "/docs/getting-started", title: "Getting started" },
    ],
  },
  {
    title: "Using Quack",
    pages: [
      { to: "/docs/rules", title: "Rules" },
      { to: "/docs/cases", title: "Cases" },
      { to: "/docs/appeals", title: "Appeals" },
      { to: "/docs/extras", title: "Tickets, logs & more" },
    ],
  },
  {
    title: "Reference",
    pages: [
      { to: "/docs/permissions", title: "Who can do what" },
      { to: "/docs/commands", title: "Commands" },
      { to: "/docs/faq", title: "Common questions" },
    ],
  },
] as const;

/** DocPath is the address of one docs page. */
export type DocPath = (typeof docGroups)[number]["pages"][number]["to"];

/** docPages is every docs page in reading order. */
export const docPages: readonly { to: DocPath; title: string }[] = docGroups.flatMap(
  (g): readonly { to: DocPath; title: string }[] => g.pages,
);

/** neighbors finds the pages before and after path, for the footer links. */
export function neighbors(path: string) {
  const clean = path.length > 1 ? path.replace(/\/+$/, "") : path;
  const i = docPages.findIndex((p) => p.to === clean);
  if (i < 0) return { prev: undefined, next: undefined };
  return { prev: docPages[i - 1], next: docPages[i + 1] };
}
