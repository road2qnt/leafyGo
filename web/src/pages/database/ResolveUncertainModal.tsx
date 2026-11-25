import { AlertTriangle, ShieldQuestion } from "lucide-react";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router";

import { migrationApi } from "../../api/endpoints";
import {
  Button,
  Callout,
  ErrorBanner,
  Field,
  Modal,
  OptionalTag,
  Textarea,
} from "../../components/ui";
import { useToast } from "../../hooks/useToast";

export function ResolveUncertainModal({
  open,
  onClose,
  dbId,
  migrationId,
  executionId,
}: {
  open: boolean;
  onClose: () => void;
  dbId: string;
  migrationId: string;
  executionId: string;
}) {
  const navigate = useNavigate();
  const toast = useToast();
  const [resolution, setResolution] = useState<"committed" | "not_committed">("committed");
  const [reason, setReason] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) {
      setResolution("committed");
      setReason("");
      setError(null);
      setSaving(false);
    }
  }, [open]);

  const submit = async () => {
    if (!reason.trim()) {
      setError(new Error("A reason is mandatory for resolve_uncertain."));
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const proposal = await migrationApi.createProposal(dbId, {
        migration_id: migrationId,
        action_type: "resolve_uncertain",
        target_execution_id: executionId,
        resolution,
        reason,
      });
      toast.success("Resolution submitted", "It still needs full group-leader approval.");
      onClose();
      navigate(`/databases/${dbId}/proposals/${proposal.id}`);
    } catch (cause) {
      setError(cause);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={
        <span className="inline-flex flex-wrap items-center gap-2">
          Resolve uncertain execution <OptionalTag />
        </span>
      }
      description="This is the optional manual exit from uncertain_hold. Automatic re-probe is the default exit."
      size="md"
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={saving}>Cancel</Button>
          <Button variant="danger" onClick={() => void submit()} loading={saving}>
            <ShieldQuestion className="h-4 w-4" /> Submit resolution
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Callout tone="plum" title="Evidence wins">
          After the old target session is proven dead, the worker reads{" "}
          <code className="font-mono text-xs">_leafygo_executions</code>. If your resolution contradicts
          the evidence, the job fails with{" "}
          <code className="font-mono text-xs">RESOLVE_UNCERTAIN_INVALID</code> and the database stays
          locked.
        </Callout>

        {error ? <ErrorBanner error={error} /> : null}

        <div>
          <span className="label">Resolution</span>
          <div className="grid gap-2 sm:grid-cols-2">
            {([
              { value: "committed", title: "Committed", description: "Mark the target execution as success." },
              { value: "not_committed", title: "Not committed", description: "Mark it failed; release or hold per batches." },
            ] as const).map((option) => (
              <button
                key={option.value}
                type="button"
                onClick={() => setResolution(option.value)}
                className={`rounded-xl border p-3 text-left transition ${
                  resolution === option.value
                    ? "border-[#8f6bb0] bg-[#f4eefa]"
                    : "border-line bg-surface hover:border-[#c9a7d8]"
                }`}
              >
                <span className="block text-sm font-semibold text-ink">{option.title}</span>
                <span className="mt-0.5 block text-[11px] leading-relaxed text-muted">{option.description}</span>
              </button>
            ))}
          </div>
        </div>

        <Field label="Reason (required)" htmlFor="resolve-reason" required hint="Explain how you established the outcome; this is written to history.">
          <Textarea
            id="resolve-reason"
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            placeholder="Target restored from backup at 14:02; evidence table has no row for this execution."
            className="min-h-24"
          />
        </Field>

        <div className="flex items-start gap-2 rounded-lg border border-[#ead9ab] bg-[#fbf4dd] px-3 py-2 text-[11px] leading-relaxed text-[#5f4810]">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          The proposal still requires every required group leader to approve. Submitting is not a
          unilateral override.
        </div>

        <p className="font-mono text-[10px] text-muted">target_execution_id: {executionId}</p>
      </div>
    </Modal>
  );
}
