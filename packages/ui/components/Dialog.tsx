import { useEffect, useRef, type ReactNode } from "react";
import "./Dialog.css";

// A focused window over the page for one change: a title, one line on what it does, the form, and its actions at
// the foot. It is the browser's own modal dialog, so focus stays inside, Escape and the backdrop close it, and the
// page behind keeps its place. Its body is mounted only while it is open, so each opening starts afresh.
export function Dialog({ open, onClose, title, description, children }: { open: boolean; onClose: () => void; title: ReactNode; description?: ReactNode; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    else if (!open && dialog.open) dialog.close();
  }, [open]);
  return (
    <dialog ref={ref} className="dialog" onClose={onClose} onClick={(event) => event.target === event.currentTarget && onClose()}>
      {open && (
        <div className="dialog-frame">
          <div className="dialog-head">
            <div>
              <h2>{title}</h2>
              {description && <p>{description}</p>}
            </div>
            <button type="button" className="dialog-close" aria-label="Close" onClick={onClose}>
              <svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none">
                <path d="M19 5L5 19M5 5l14 14" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
              </svg>
            </button>
          </div>
          <div className="dialog-body">{children}</div>
        </div>
      )}
    </dialog>
  );
}

// The dialog's actions, at its foot: the one primary action on the right, the way out beside it.
export function DialogActions({ children }: { children: ReactNode }) {
  return <div className="dialog-actions">{children}</div>;
}
