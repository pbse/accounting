import { Icon, type IconName } from "./Icon";
import "./IconTile.css";

export type Tone = "blue" | "orange" | "teal" | "red" | "gray";

// A 32px glossy tile holding a 16px icon, used at the head of stat tiles and rows.
export function IconTile({ icon, tone = "blue" }: { icon: IconName; tone?: Tone }) {
  return (
    <span className={`icon-tile icon-tile-${tone}`} aria-hidden="true">
      <Icon name={icon} />
    </span>
  );
}
