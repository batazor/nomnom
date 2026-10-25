import { m } from "motion/react";

export type DishIconProps = Readonly<{
  name: string;
  // The same dish on the restaurant card and in its menu is one element, so
  // the icon flies between them like an app out of an iOS folder.
  restaurantId: string;
  itemId: string;
  size: "xs" | "md";
}>;

const tones = 4;

// A dish without a photo: the first letter of its name on a tinted tile. The
// tint comes from the name, so a dish keeps its color everywhere. Decorative:
// the name next to it carries the meaning.
export function DishIcon({ name, restaurantId, itemId, size }: DishIconProps) {
  const letter = name.trim().charAt(0).toLocaleUpperCase();
  let hash = 0;
  for (const character of name)
    hash = (hash * 31 + character.charCodeAt(0)) | 0;

  return (
    <m.span
      aria-hidden="true"
      className="nomnom-dish-icon"
      data-size={size}
      data-tone={Math.abs(hash) % tones}
      layoutId={`dish-${restaurantId}-${itemId}`}
      transition={{ type: "spring", bounce: 0.2, duration: 0.45 }}
    >
      <m.span layout="position">{letter}</m.span>
    </m.span>
  );
}
