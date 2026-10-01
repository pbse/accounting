import "./Avatar.css";

const tones = ["teal", "pink", "blue", "orange", "violet"];

// A client's initial on a coloured tile; the same label always gets the same colour.
export function Avatar({ label }: { label: string }) {
  let hash = 0;
  for (const character of label) hash = (hash * 31 + character.charCodeAt(0)) >>> 0;
  return (
    <span className={`avatar avatar-${tones[hash % tones.length]}`} aria-hidden="true">
      {label.trim().charAt(0).toUpperCase() || "?"}
    </span>
  );
}
