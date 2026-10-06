import { useEffect } from "react";

/**
 * useTitle names the browser tab while a page is mounted and restores the
 * previous title when it leaves, so dashboard pages keep the default.
 */
export function useTitle(title: string) {
  useEffect(() => {
    const previous = document.title;
    document.title = title;
    return () => {
      document.title = previous;
    };
  }, [title]);
}
