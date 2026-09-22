import { useEffect, useState, type RefObject } from "react";
import { useSearchParams } from "react-router";
import { workspaceRequest } from "./workspace";
import type { EvidenceFile, Source } from "./Evidence";

// Reuse the preparation forms; confirmations deliberately never carry forward.
export function useRevision(form: RefObject<HTMLFormElement | null>) {
  const [params] = useSearchParams();
  const close = params.get("close");
  const paper = params.get("revise");
  const [ready, setReady] = useState(!paper);
  const [error, setError] = useState("");
  const [sources, setSources] = useState<Source[]>([]);
  useEffect(() => {
    if (!paper || !close) return;
    let cancelled = false;
    setReady(false);
    Promise.all([
      workspaceRequest(`/${close}/workpapers/${paper}`),
      workspaceRequest(`/${close}/evidence`),
    ])
      .then(
        async ([saved, files]: [
          { input: Record<string, unknown>; sources?: Source[] },
          EvidenceFile[],
        ]) => {
          const input = { ...saved.input };
          const updated: Source[] = [];
          for (const source of saved.sources ?? []) {
            let current = files.find((f) => f.id === source.id);
            while (current?.superseded_by) {
              const next = current.superseded_by;
              current = files.find((f) => f.id === next);
            }
            if (!current || current.extraction_state !== "ready")
              throw new Error(
                "Wait for current evidence extraction before revising.",
              );
            updated.push({
              ...source,
              ...current,
              location: current.id === source.id ? source.location : "",
            });
            if (source.role === "statement" || source.role === "ledger") {
              const text = await workspaceRequest(
                `/${close}/evidence/${current.id}/text`,
                undefined,
                true,
              );
              input[`${source.role}_csv`] = await text.text();
              input[`${source.role}_reference`] = current.name;
            }
          }
          if (cancelled) return;
          for (const [name, value] of Object.entries(input)) {
            const field = form.current?.elements.namedItem(name);
            if (field instanceof HTMLInputElement && field.type === "checkbox")
              field.checked = false;
            else if (
              field instanceof HTMLInputElement ||
              field instanceof HTMLTextAreaElement || field instanceof HTMLSelectElement
            )
              field.value = Array.isArray(value)
                ? value.join(",")
                : String(value);
          }
          setSources(updated);
          setReady(true);
        },
      )
      .catch((e) => {
        if (!cancelled) setError(e.message);
      });
    return () => {
      cancelled = true;
    };
  }, [close, paper, form]);
  return { ready, error, sources, paper, close };
}
