import { useQueryClient } from "@tanstack/react-query";

import { api, loginUrl } from "./client";

/**
 * useSignInAgain ends the session and starts Discord sign-in straight away,
 * coming back to the current page. Quack reads facts it can only learn at
 * sign-in, like whether the user has 2FA, so this is how they refresh.
 */
export function useSignInAgain() {
  const queryClient = useQueryClient();
  return async () => {
    const here = window.location.pathname + window.location.search;
    await api.POST("/auth/logout").catch(() => undefined);
    queryClient.clear();
    window.location.assign(loginUrl(here));
  };
}
