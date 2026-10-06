import { describe as group, expect, it } from "vite-plus/test";

import type { AuditEntry } from "~/api/types";

import {
  auditActionGroups,
  auditQueryOf,
  caseRefOf,
  describe,
  metadataRows,
  sentence,
} from "./describe";

type Entry = Pick<
  AuditEntry,
  "action" | "result" | "metadata" | "resource_type" | "resource_id" | "failure_reason"
>;

const entry = (patch: Partial<Entry>): Entry => ({
  action: "case.void",
  result: "success",
  metadata: {},
  resource_type: "case",
  resource_id: "01HCASE",
  ...patch,
});

group("describe", () => {
  it("names the case by number when metadata has one", () => {
    const d = describe(entry({ metadata: { case_number: 12, case_id: "01HCASE" } }));
    expect(sentence(d)).toBe("voided case #12");
    expect(d.parts[1]).toEqual({ kind: "case", label: "case #12", ref: "12" });
    expect(d.icon).toBe("case_void");
  });

  it("links a case resource without a number by its ID", () => {
    const d = describe(entry({ action: "case.read" }));
    expect(sentence(d)).toBe("viewed a case");
    expect(d.parts[1]).toEqual({ kind: "case", label: "a case", ref: "01HCASE" });
  });

  it("fills in the Discord action", () => {
    const d = describe(
      entry({
        action: "case_action.failed",
        result: "failure",
        resource_type: "case_action_execution",
        metadata: { case_number: 4, action_type: "ban_user" },
      }),
    );
    expect(sentence(d)).toBe("couldn't complete the ban on case #4");
    expect(d.icon).toBe("error");
  });

  it("says when a reversal had nothing left to undo", () => {
    const d = describe(
      entry({ action: "case_action.succeeded", metadata: { case_number: 3, reversal_noop: true } }),
    );
    expect(sentence(d)).toBe("found the punishment on case #3 had already ended");
  });

  it("words denials", () => {
    expect(sentence(describe(entry({ action: "case.void", result: "denied" })))).toBe(
      "was denied permission to void a case",
    );
    expect(
      sentence(
        describe(
          entry({
            action: "authorization.denied",
            result: "denied",
            resource_type: "permission",
            resource_id: "case.create",
          }),
        ),
      ),
    ).toBe("was denied permission to open cases");
  });

  it("blames Quack for denials about its own access", () => {
    expect(
      sentence(
        describe(
          entry({
            action: "authorization.denied",
            result: "denied",
            resource_type: "permission",
            resource_id: "case.create",
            failure_reason: "bot_permission_required",
          }),
        ),
      ),
    ).toBe("couldn't open cases: Quack lacks the permission or role position");
    expect(
      sentence(
        describe(entry({ action: "case.void", result: "denied", failure_reason: "bot_hierarchy" })),
      ),
    ).toBe("couldn't void a case: Quack lacks the permission or role position");
  });

  it("links appeals", () => {
    const d = describe(
      entry({ action: "appeal.accepted", resource_type: "appeal", resource_id: "01HAPPEAL" }),
    );
    expect(sentence(d)).toBe("accepted an appeal");
    expect(d.parts[1]).toEqual({ kind: "appeal", label: "an appeal", ref: "01HAPPEAL" });
  });

  it("falls back to the action name", () => {
    expect(sentence(describe(entry({ action: "widget.spin", resource_type: "widget" })))).toBe(
      "recorded widget spin",
    );
    expect(
      sentence(describe(entry({ action: "widget.spin", result: "failure", resource_type: "x" }))),
    ).toBe("couldn't widget spin");
  });
});

group("caseRefOf", () => {
  it("ignores placeholder resource IDs", () => {
    expect(caseRefOf(entry({ resource_id: "unknown" }))).toBeNull();
    expect(caseRefOf(entry({ resource_type: "appeal" }))).toBeNull();
  });
});

group("metadataRows", () => {
  it("flattens nested objects and formats values", () => {
    expect(metadataRows({ partial: false, v4: { cases: 3 }, ids: ["a", "b"], note: null })).toEqual(
      [
        { key: "partial", label: "Partial", value: "No" },
        { key: "v4.cases", label: "V4 · cases", value: "3" },
        { key: "ids", label: "Ids", value: "a, b" },
        { key: "note", label: "Note", value: "None" },
      ],
    );
  });
  it("returns nothing for non-objects", () => {
    expect(metadataRows(null)).toEqual([]);
    expect(metadataRows([1, 2])).toEqual([]);
  });
});

group("auditQueryOf", () => {
  it("makes the date range cover whole local days", () => {
    const q = auditQueryOf({ from: "2026-10-01", to: "2026-10-02" });
    expect(q.created_after).toBe(new Date(2026, 9, 1).toISOString());
    expect(q.created_before).toBe(new Date(2026, 9, 3).toISOString());
  });
  it("passes the other filters through", () => {
    expect(auditQueryOf({ actor: "1", member: "2", result: "denied" })).toMatchObject({
      actor_discord_user_id: "1",
      member_discord_user_id: "2",
      result: "denied",
    });
  });
});

group("auditActionGroups", () => {
  it("puts every known action in a group", () => {
    const all = auditActionGroups.flatMap((g) => g.actions.map((a) => a.value));
    expect(all).toContain("case.void");
    expect(all).toContain("ticket.open");
    expect(all).not.toContain("case.read");
    expect(all).not.toContain("audit.read");
    expect(auditActionGroups.find((g) => g.label === "Appeals")?.actions.length).toBeGreaterThan(5);
  });
});
