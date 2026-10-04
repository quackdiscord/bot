import createClient, { type Middleware } from "openapi-fetch";

import type { components, paths } from "./schema.gen";

/** ApiErrorCode is the machine-readable error code in every error envelope. */
export type ApiErrorCode = components["schemas"]["ApiErrorCode"];

/**
 * ApiError is a failed API call. The backend wraps every failure in the same
 * envelope, so message is always safe to show to the user.
 */
export class ApiError extends Error {
  readonly status: number;
  readonly code: ApiErrorCode | "network_error";
  readonly requestId?: string;

  constructor(status: number, code: ApiError["code"], message: string, requestId?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.requestId = requestId;
  }

  /** Whether the session is gone and the user has to sign in again. */
  get signedOut(): boolean {
    return (
      this.status === 401 ||
      this.code === "authentication_required" ||
      this.code === "reauthentication_required"
    );
  }
}

let csrfToken = "";

/**
 * setCsrfToken stores the token from GET /auth/me. Cookie-authenticated
 * writes must echo it in X-CSRF-Token.
 */
export function setCsrfToken(token: string) {
  csrfToken = token;
}

const writeMethods = new Set(["POST", "PUT", "PATCH", "DELETE"]);

const writeHeaders: Middleware = {
  onRequest({ request }) {
    if (!writeMethods.has(request.method)) return request;
    if (csrfToken) request.headers.set("X-CSRF-Token", csrfToken);
    // Every staff and member write is idempotent on the server. One key per
    // call protects against double submits and lets a retry replay safely.
    if (!request.headers.has("Idempotency-Key")) {
      request.headers.set("Idempotency-Key", crypto.randomUUID());
    }
    return request;
  },
};

/**
 * Paths is the generated contract with every header parameter optional: the
 * middleware above supplies X-CSRF-Token and Idempotency-Key on every write.
 */
type Paths = {
  [P in keyof paths]: {
    [M in keyof paths[P]]: paths[P][M] extends { parameters: infer Params }
      ? Omit<paths[P][M], "parameters"> & {
          parameters: Omit<Params, "header"> & { header?: never };
        }
      : paths[P][M];
  };
};

/** api is the typed client for the Quack HTTP API, served under /api. */
export const api = createClient<Paths>({
  baseUrl: "/api",
  credentials: "same-origin",
  headers: { Accept: "application/json" },
});
api.use(writeHeaders);

type Result<T> = { data?: T; error?: unknown; response: Response };

/**
 * unwrap turns an openapi-fetch result into its data, throwing ApiError for
 * any failure so TanStack Query sees a rejected promise.
 */
export async function unwrap<T>(pending: Promise<Result<T>>): Promise<T> {
  let result: Result<T>;
  try {
    result = await pending;
  } catch {
    throw new ApiError(0, "network_error", "Can't reach Quack right now. Check your connection.");
  }
  const { data, error, response } = result;
  if (response.ok) return data as T;
  const detail = (error as { error?: components["schemas"]["ApiErrorDetail"] } | undefined)?.error;
  throw new ApiError(
    response.status,
    detail?.code ?? "internal_error",
    detail?.message ? sentence(detail.message) : fallbackMessage(response.status),
    detail?.request_id,
  );
}

function sentence(message: string): string {
  const trimmed = message.trim();
  const capitalized = trimmed.charAt(0).toUpperCase() + trimmed.slice(1);
  return /[.!?]$/.test(capitalized) ? capitalized : `${capitalized}.`;
}

function fallbackMessage(status: number): string {
  if (status === 429) return "You're going a little fast. Try again in a moment.";
  if (status >= 500) return "Quack hit a snag. Try again in a moment.";
  return "That didn't work.";
}

/** loginUrl starts Discord sign-in and returns to redirectTo afterwards. */
export function loginUrl(redirectTo = "/guilds"): string {
  return `/api/auth/discord/login?${new URLSearchParams({ redirect_to: redirectTo })}`;
}
