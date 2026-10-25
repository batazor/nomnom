import type { StatusIconName } from "./order-presentation";

const paths: Record<StatusIconName, string> = {
  receipt: "M7 3h10v18l-2.5-1.5L12 21l-2.5-1.5L7 21zM10 8h4M10 12h4",
  check: "M5 12.5l4.5 4.5L19 7.5",
  pot: "M4 11h16M6 11v6a3 3 0 003 3h6a3 3 0 003-3v-6M9 7.5c0-1 1-1.5 1-2.5M14 7.5c0-1 1-1.5 1-2.5",
  bag: "M6 8h12l-1 12H7zM9 8a3 3 0 016 0",
  bike: "M6 17a3 3 0 100-.01M18 17a3 3 0 100-.01M6 17l4-7h5l3 7M10 10l-1.5-3H6",
  home: "M4 11l8-7 8 7M6 10v10h12V10M10 20v-5h4v5",
  cross: "M7 7l10 10M17 7L7 17",
  alert: "M12 4l9 16H3zM12 10v4M12 17v.5",
};

export function StatusIcon({ name }: { name: StatusIconName }) {
  return (
    <svg
      aria-hidden="true"
      className="status-icon"
      fill="none"
      focusable="false"
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth="1.75"
      viewBox="0 0 24 24"
    >
      <path d={paths[name]} />
    </svg>
  );
}
