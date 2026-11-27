import { useState } from "react";

export const PAGE_SIZES = [5, 10, 25, 50, 100];

/** Same key as the Milestone 1 pages, so the chosen size carries across both. */
const STORAGE_KEY = "leafygo_page_size";

function storedPageSize(): number | null {
  try {
    const size = Number(window.localStorage.getItem(STORAGE_KEY));
    return PAGE_SIZES.includes(size) ? size : null;
  } catch {
    return null;
  }
}

/**
 * Client-side pagination for a list already in memory.
 * The view returns to page 1 when `resetKey` changes (a filter, another database),
 * but stays put when the same list is only refetched.
 */
export function usePagination<T>(
  items: T[],
  { defaultSize = 10, resetKey }: { defaultSize?: number; resetKey?: unknown } = {},
) {
  const [pageSize, setSize] = useState(() => storedPageSize() ?? defaultSize);
  const [page, setPage] = useState(1);
  const [seenKey, setSeenKey] = useState(resetKey);

  if (!Object.is(seenKey, resetKey)) {
    setSeenKey(resetKey);
    setPage(1);
  }

  const totalPages = Math.max(1, Math.ceil(items.length / pageSize));
  const currentPage = Math.min(page, totalPages);
  const start = (currentPage - 1) * pageSize;
  const pageItems = items.slice(start, start + pageSize);

  const setPageSize = (size: number) => {
    setSize(size);
    setPage(1);
    try {
      window.localStorage.setItem(STORAGE_KEY, String(size));
    } catch {
      // Storage can be unavailable; the size then lasts for this view only.
    }
  };

  return {
    pageItems,
    /** Props for <Pagination>. */
    controls: {
      page: currentPage,
      totalPages,
      pageSize,
      onPageChange: setPage,
      onPageSizeChange: setPageSize,
      from: items.length === 0 ? 0 : start + 1,
      to: start + pageItems.length,
      total: items.length,
    },
  };
}
