import {
  animate,
  m,
  useMotionValue,
  useReducedMotion,
  useSpring,
} from "motion/react";
import { useEffect, useState } from "react";

// What the halo wraps on hover. Text fields keep the bare I-beam.
const targetSelector =
  'button:not(:disabled), a[href], [role="button"], [role="switch"], [draggable="true"], label';
const textSelector = 'input, textarea, [contenteditable="true"]';

const dot = 28;
const follow = { stiffness: 600, damping: 40, mass: 0.4 };

// A soft ring that trails the mouse pointer and snaps around the button or
// link under it, shrinking while pressed. It adds to the system cursor rather
// than replacing it, and is there only for a precise pointer and when the user
// does not ask for reduced motion.
export function PointerHalo() {
  const reduced = useReducedMotion();
  const [finePointer, setFinePointer] = useState(false);

  useEffect(() => {
    const query = window.matchMedia("(hover: hover) and (pointer: fine)");
    setFinePointer(query.matches);
    const changed = () => setFinePointer(query.matches);
    query.addEventListener("change", changed);
    return () => query.removeEventListener("change", changed);
  }, []);

  if (reduced || !finePointer) return null;
  return <Halo />;
}

function Halo() {
  const x = useSpring(useMotionValue(-dot), follow);
  const y = useSpring(useMotionValue(-dot), follow);
  const width = useSpring(dot, follow);
  const height = useSpring(dot, follow);
  const radius = useSpring(dot / 2, follow);
  const scale = useMotionValue(1);
  const opacity = useMotionValue(0);

  useEffect(() => {
    let target: Element | null = null;

    function wrap(element: Element) {
      const box = element.getBoundingClientRect();
      const pad = 4;
      x.set(box.left - pad);
      y.set(box.top - pad);
      width.set(box.width + pad * 2);
      height.set(box.height + pad * 2);
      radius.set(
        Number.parseFloat(getComputedStyle(element).borderRadius) + pad,
      );
    }

    function moved(event: PointerEvent) {
      if (event.pointerType !== "mouse") return;
      const under = event.target instanceof Element ? event.target : null;
      const overText = under?.closest(textSelector);
      target = overText ? null : (under?.closest(targetSelector) ?? null);
      opacity.set(overText ? 0 : 1);
      if (target) {
        wrap(target);
        return;
      }
      x.set(event.clientX - dot / 2);
      y.set(event.clientY - dot / 2);
      width.set(dot);
      height.set(dot);
      radius.set(dot / 2);
    }

    function scrolled() {
      if (target) wrap(target);
    }

    const left = () => opacity.set(0);
    const pressed = () => animate(scale, 0.92, { duration: 0.1 });
    const released = () =>
      animate(scale, 1, { type: "spring", bounce: 0.4, duration: 0.3 });

    window.addEventListener("pointermove", moved);
    window.addEventListener("pointerdown", pressed);
    window.addEventListener("pointerup", released);
    window.addEventListener("scroll", scrolled, true);
    document.documentElement.addEventListener("pointerleave", left);
    return () => {
      window.removeEventListener("pointermove", moved);
      window.removeEventListener("pointerdown", pressed);
      window.removeEventListener("pointerup", released);
      window.removeEventListener("scroll", scrolled, true);
      document.documentElement.removeEventListener("pointerleave", left);
    };
  }, [x, y, width, height, radius, scale, opacity]);

  return (
    <m.div
      aria-hidden="true"
      className="nomnom-pointer-halo"
      style={{ x, y, width, height, borderRadius: radius, scale, opacity }}
    />
  );
}
