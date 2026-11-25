export type DryRunStatus = "passed" | "inconclusive" | "failed" | null | undefined;

export interface DryRunMeta {
  label: string;
  tone: "success" | "warning" | "danger" | "neutral";
  canReview: boolean;
}

export function dryRunMeta(status: DryRunStatus): DryRunMeta {
  switch (status) {
    case "passed":
      return { label: "Dry-run passed", tone: "success", canReview: true };
    case "inconclusive":
      return { label: "Dry-run inconclusive", tone: "warning", canReview: true };
    case "failed":
      return { label: "Dry-run failed", tone: "danger", canReview: false };
    default:
      return { label: "Not validated", tone: "neutral", canReview: true };
  }
}
