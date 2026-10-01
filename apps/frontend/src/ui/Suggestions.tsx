import "./Suggestions.css";

// One-click asks shown as pills; picking one hands its text to the caller, which asks it exactly as written.
export function Suggestions({ label, items, onPick, disabled = false }: { label: string; items: readonly string[]; onPick: (text: string) => void; disabled?: boolean }) {
  return (
    <span className="suggestions" role="group" aria-label={label}>
      {items.map((text) => (
        <button key={text} type="button" disabled={disabled} onClick={() => onPick(text)}>
          {text}
        </button>
      ))}
    </span>
  );
}
