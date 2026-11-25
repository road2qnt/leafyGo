import { createContext, useContext } from "react";

import type { DatabaseOverview } from "../../api/types";

export interface DatabaseContextValue {
  overview: DatabaseOverview;
  refetch: () => void;
}

export const DatabaseContext = createContext<DatabaseContextValue | null>(null);

export function useDatabaseContext(): DatabaseContextValue {
  const context = useContext(DatabaseContext);
  if (!context) {
    throw new Error("useDatabaseContext must be used inside <DatabaseLayout>");
  }
  return context;
}
