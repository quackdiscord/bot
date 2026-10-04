/**
 * createSubmissionGate blocks overlapping submits before React renders a
 * pending button. Success locks the form through its close animation; reset
 * starts a new opening. Failed requests remain retryable.
 */
export function createSubmissionGate() {
  let pending = false;
  let completed = false;
  return {
    async submit<T>(submit: () => Promise<T>): Promise<T | undefined> {
      if (pending || completed) return undefined;
      pending = true;
      try {
        const result = await submit();
        completed = true;
        return result;
      } finally {
        pending = false;
      }
    },
    reset() {
      // Closing and reopening cannot unlock a request still in flight.
      if (!pending) completed = false;
    },
  };
}
