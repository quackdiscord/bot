import { describe, expect, it, vi } from "vite-plus/test";

import { createSubmissionGate } from "./submission";

function deferred() {
  let resolve!: () => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<void>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

describe("case submission", () => {
  it("starts only one request for synchronous and overlapping submits", async () => {
    const gate = createSubmissionGate();
    const request = deferred();
    const submit = vi.fn(() => request.promise);
    const first = gate.submit(submit);
    const second = gate.submit(submit);
    await Promise.resolve();
    const third = gate.submit(submit);
    expect(submit).toHaveBeenCalledTimes(1);
    await expect(second).resolves.toBeUndefined();
    await expect(third).resolves.toBeUndefined();
    request.resolve();
    await first;
  });

  it("allows another attempt after a failed request", async () => {
    const gate = createSubmissionGate();
    const request = deferred();
    const submit = vi.fn(() => request.promise);
    const first = gate.submit(submit);
    const failed = expect(first).rejects.toThrow("network unavailable");
    await gate.submit(submit);
    request.reject(new Error("network unavailable"));
    await failed;
    const retry = vi.fn().mockResolvedValue("created");
    await expect(gate.submit(retry)).resolves.toBe("created");
    expect(submit).toHaveBeenCalledTimes(1);
    expect(retry).toHaveBeenCalledTimes(1);
  });

  it("releases the guard after synchronous failures", async () => {
    const gate = createSubmissionGate();
    await expect(
      gate.submit(() => {
        throw new Error("invalid input");
      }),
    ).rejects.toThrow("invalid input");
    await expect(gate.submit(() => Promise.resolve("created"))).resolves.toBe("created");
  });
  it("keeps a fast successful submission locked through closing until the next opening", async () => {
    const gate = createSubmissionGate();
    const submit = vi.fn().mockResolvedValue("created");
    await expect(gate.submit(submit)).resolves.toBe("created");
    // Pending is already false, but the closing dialog still has a form.
    await expect(gate.submit(submit)).resolves.toBeUndefined();
    expect(submit).toHaveBeenCalledTimes(1);
    gate.reset();
    await expect(gate.submit(submit)).resolves.toBe("created");
    expect(submit).toHaveBeenCalledTimes(2);
  });

  it("does not unlock an overlapping request when the dialog reopens", async () => {
    const gate = createSubmissionGate();
    const request = deferred();
    const submit = vi.fn(() => request.promise);
    const first = gate.submit(submit);
    gate.reset();
    await expect(gate.submit(submit)).resolves.toBeUndefined();
    expect(submit).toHaveBeenCalledTimes(1);
    request.resolve();
    await first;
    await expect(gate.submit(submit)).resolves.toBeUndefined();
  });
});
