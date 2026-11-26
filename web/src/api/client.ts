import type { ApiErrorBody } from "./types";

let csrfToken = "";

export function setCsrfToken(token: string | null | undefined) {
  csrfToken = token ?? "";
}

export function getCsrfToken() {
  return csrfToken;
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly correlationId?: string;
  readonly traceId?: string;

  constructor(
    message: string,
    options: { status: number; code?: string; correlationId?: string; traceId?: string },
  ) {
    super(message);
    this.name = "ApiError";
    this.status = options.status;
    this.code = options.code ?? "UNKNOWN";
    this.correlationId = options.correlationId;
    this.traceId = options.traceId;
  }
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError;
}

export interface RequestOptions {
  method?: "GET" | "POST" | "PUT" | "DELETE";
  json?: unknown;
  signal?: AbortSignal;
}

export async function api<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? (options.json === undefined ? "GET" : "POST");
  const headers = new Headers({ Accept: "application/json" });
  let body: BodyInit | undefined;

  if (options.json !== undefined) {
    headers.set("Content-Type", "application/json");
    body = JSON.stringify(options.json);
  }
  if (csrfToken && method !== "GET") {
    headers.set("X-CSRF-Token", csrfToken);
  }

  let response: Response;
  try {
    response = await fetch(`/api/v1${path}`, {
      method,
      headers,
      body,
      credentials: "same-origin",
      signal: options.signal,
    });
  } catch (cause) {
    throw new ApiError("The API is unreachable. Check your connection and try again.", {
      status: 0,
      code: "NETWORK_ERROR",
    });
  }

  if (response.status === 204) {
    return undefined as T;
  }

  const raw = await response.text();
  let parsed: unknown = null;
  if (raw) {
    try {
      parsed = JSON.parse(raw);
    } catch {
      parsed = null;
    }
  }

  if (!response.ok) {
    if (response.status === 401 && !path.startsWith("/auth/login") && !path.startsWith("/auth/me")) {
      window.dispatchEvent(new CustomEvent("leafygo:unauthorized"));
    }
    const envelope = parsed as ApiErrorBody | null;
    throw new ApiError(
      envelope?.error?.message ?? `Request failed with status ${response.status}.`,
      {
        status: response.status,
        code: envelope?.error?.code,
        correlationId: envelope?.error?.correlation_id,
        traceId: envelope?.error?.trace_id,
      },
    );
  }

  return parsed as T;
}

export function queryString(params: Record<string, string | number | boolean | undefined | null>) {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === "") continue;
    search.set(key, String(value));
  }
  const encoded = search.toString();
  return encoded ? `?${encoded}` : "";
}
