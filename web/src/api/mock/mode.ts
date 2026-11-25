const MOCK_STORAGE_KEY = "leafygo-mock";

/**
 * Mocks are enabled by:
 *  - VITE_USE_MOCKS=true (dedicated demo build), or
 *  - ?mock=1 on any SPA URL (persisted in localStorage), or
 *  - a previous ?mock=1 visit.
 *
 * The default production bundle therefore talks to the real /api/v1 endpoints;
 * the in-browser mock chunk is only fetched for demos.
 */
export function isMockMode(): boolean {
  if (import.meta.env.VITE_USE_MOCKS === "true") {
    return true;
  }
  try {
    const params = new URLSearchParams(window.location.search);
    const flag = params.get("mock");
    if (flag === "1") {
      window.localStorage.setItem(MOCK_STORAGE_KEY, "1");
      return true;
    }
    if (flag === "0") {
      window.localStorage.removeItem(MOCK_STORAGE_KEY);
      return false;
    }
    return window.localStorage.getItem(MOCK_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}
