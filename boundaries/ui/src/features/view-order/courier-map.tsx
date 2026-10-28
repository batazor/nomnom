import { m } from "motion/react";
import { useMemo } from "react";

import { useI18n } from "../../shared/i18n/i18n";
import { type GridPoint, toGrid } from "../../shared/virtual-city";
import {
  cityBlocks,
  type OrderTracking,
  type TrackedPosition,
  useCourierTrack,
} from "./courier-track";

// Gap of the streets between blocks, in blocks.
const street = 0.16;
// Bounds of a marker move: points arrive about once a second (FR-667).
const minMoveMs = 200;
const maxMoveMs = 3_000;

// Live map of the Virtual City (FR-664): streets, blocks, restaurant,
// customer, the courier and its trail. Only the marker animates; data comes
// from the query and courierMoved, never from a timer (ADR 36).
export function CourierMap({
  tracking,
  pickedUp,
}: {
  tracking: OrderTracking;
  pickedUp: boolean;
}) {
  const { t } = useI18n();
  const trail = useCourierTrack(tracking);
  const { city } = tracking;
  const size = city.blocks;
  // SVG y grows downwards, the grid's northwards.
  const svg = (point: GridPoint) => ({ x: point.x, y: size - point.y });
  const pickup = svg(toGrid(city, tracking.pickup));
  const dropoff = svg(toGrid(city, tracking.dropoff));
  const path = trail.map((position) => svg(toGrid(city, position)));
  const latest = path.at(-1) ?? null;
  const blocks = useMemo(
    () => cityBlocks(size, tracking.channel),
    [size, tracking.channel],
  );
  const phase = pickedUp
    ? t("order.map.toCustomer")
    : t("order.map.toRestaurant");

  return (
    <section aria-labelledby="order-map-title" className="order-card order-map">
      <div className="order-map-heading">
        <h4 id="order-map-title">{t("order.map.title")}</h4>
        <p className="order-map-phase">{phase}</p>
      </div>
      <svg
        aria-label={phase}
        className="order-map-canvas"
        role="img"
        viewBox={`${-street} ${-street} ${size + 2 * street} ${size + 2 * street}`}
      >
        <rect
          className="order-map-street"
          height={size + 2 * street}
          width={size + 2 * street}
          x={-street}
          y={-street}
        />
        {blocks.map((block) => (
          <rect
            className="order-map-block"
            data-kind={block.kind}
            height={1 - street}
            key={`${block.x}-${block.y}`}
            rx={0.08}
            width={1 - street}
            x={block.x + street / 2}
            y={size - block.y - 1 + street / 2}
          />
        ))}
        {path.length > 1 ? (
          <polyline
            className="order-map-trail"
            points={path.map((point) => `${point.x},${point.y}`).join(" ")}
          />
        ) : null}
        <Place
          className="order-map-pickup"
          label={t("order.map.restaurant")}
          point={pickup}
        />
        <Place
          className="order-map-dropoff"
          label={t("order.map.customer")}
          point={dropoff}
        />
        {latest ? (
          // The marker covers each step in the time the courier took; under
          // prefers-reduced-motion MotionConfig makes it jump.
          <m.g
            animate={{ x: latest.x, y: latest.y }}
            className="order-map-courier"
            data-testid="courier-marker"
            initial={false}
            transition={{
              duration: moveDuration(trail) / 1000,
              ease: "linear",
            }}
          >
            <title>{t("order.map.courier")}</title>
            <circle className="order-map-courier-halo" r={0.42} />
            <circle className="order-map-courier-dot" r={0.22} />
          </m.g>
        ) : null}
      </svg>
    </section>
  );
}

function Place({
  point,
  label,
  className,
}: {
  point: GridPoint;
  label: string;
  className: string;
}) {
  return (
    <g className={className} transform={`translate(${point.x} ${point.y})`}>
      <title>{label}</title>
      <circle r={0.3} />
    </g>
  );
}

// Time between the last two points: the marker covers the step as the
// courier did.
function moveDuration(trail: readonly TrackedPosition[]): number {
  if (trail.length < 2) return 0;
  const [previous, latest] = trail.slice(-2);
  const elapsed =
    Date.parse(latest.recordedAt) - Date.parse(previous.recordedAt);
  return Math.min(maxMoveMs, Math.max(minMoveMs, elapsed));
}
