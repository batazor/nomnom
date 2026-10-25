import "./illustration.css";

// unDraw illustrations (https://undraw.co/license), recolored: the accent is
// currentColor and the light ground follows the theme. They are inlined so
// both colors come from the tokens.
const sources = import.meta.glob<string>("./illustrations/*.svg", {
  eager: true,
  import: "default",
  query: "?raw",
});

export type IllustrationName =
  | "order-food"
  | "take-out-boxes"
  | "delivery-truck"
  | "on-the-way"
  | "order-delivered"
  | "empty-mailbox";

export type IllustrationProps = Readonly<{
  name: IllustrationName;
  size?: "sm" | "md";
}>;

// Decorative: the text next to it carries the meaning.
export function Illustration({ name, size = "md" }: IllustrationProps) {
  return (
    <span
      aria-hidden="true"
      className="illustration"
      // biome-ignore lint/security/noDangerouslySetInnerHtml: the markup is the repo's own SVG files, bundled at build time.
      dangerouslySetInnerHTML={{
        __html: sources[`./illustrations/${name}.svg`],
      }}
      data-size={size}
    />
  );
}
