import { useCallback, useSyncExternalStore } from "react";

/** Phone widths, 320 to 767px. */
export const MOBILE_QUERY = "(max-width: 47.9375rem)";

/** For behaviour that CSS alone cannot switch, such as showing a form in a dialog. */
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      const list = window.matchMedia(query);
      list.addEventListener("change", onChange);
      return () => list.removeEventListener("change", onChange);
    },
    [query],
  );
  return useSyncExternalStore(subscribe, () => window.matchMedia(query).matches);
}
