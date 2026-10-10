// A client's mark: its initial on its hue (docs/design-system.md, Mark). The same name always gets the same hue, and
// the hue is identity, never status. Styled by shell.css (.ks-mark).
const hues = ["teal", "violet", "amber", "pink", "blue", "green", "slate"];

export function Mark({ label }: { label: string }) {
  let hash = 0;
  for (const character of label) hash = (hash * 31 + character.charCodeAt(0)) >>> 0;
  return (
    <span className={`ks-mark ${hues[hash % hues.length]}`} aria-hidden="true">
      {label.trim().charAt(0).toUpperCase() || "?"}
    </span>
  );
}
