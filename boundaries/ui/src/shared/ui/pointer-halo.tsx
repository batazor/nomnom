import {
  animate,
  m,
  useMotionValue,
  useReducedMotion,
  useSpring,
} from "motion/react";
import { useEffect, useState } from "react";

// What the halo wraps on hover.
const targetSelector =
  'button:not(:disabled), a[href], [role="button"], [role="switch"], [draggable="true"], label';
// Where it turns into a text caret.
const fieldSelector = 'input, textarea, [contenteditable="true"]';
const caretWidth = 2;

const dot = 28;
const follow = { stiffness: 600, damping: 40, mass: 0.4 };

// A soft ring that trails the mouse pointer and snaps around the button or
// link under it, shrinking while pressed. Over text it turns into a caret as
// tall as the line. It adds to the system cursor rather
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
      const box = visibleBox(element);
      const pad = 4;
      x.set(box.left - pad);
      y.set(box.top - pad);
      width.set(box.right - box.left + pad * 2);
      height.set(box.bottom - box.top + pad * 2);
      radius.set(
        Number.parseFloat(getComputedStyle(element).borderRadius) + pad,
      );
    }

    function caret(pointX: number, pointY: number, line: number) {
      x.set(pointX - caretWidth / 2);
      y.set(pointY - line / 2);
      width.set(caretWidth);
      height.set(line);
      radius.set(caretWidth / 2);
    }

    function moved(event: PointerEvent) {
      if (event.pointerType !== "mouse") return;
      const under = event.target instanceof Element ? event.target : null;
      opacity.set(1);
      const field = under?.closest(fieldSelector);
      target = field ? null : (under?.closest(targetSelector) ?? null);
      if (target) {
        wrap(target);
        return;
      }
      const line = field
        ? lineHeight(field)
        : textLineAt(event.clientX, event.clientY);
      if (line) {
        caret(event.clientX, event.clientY, line);
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

// The part of the element its scrolling or clipping ancestors leave visible,
// so the halo does not wrap what a column has scrolled away.
function visibleBox(element: Element) {
  const box = element.getBoundingClientRect();
  let { left, top, right, bottom } = box;
  for (
    let parent = element.parentElement;
    parent && parent !== document.body;
    parent = parent.parentElement
  ) {
    const style = getComputedStyle(parent);
    if (style.overflowX === "visible" && style.overflowY === "visible") {
      continue;
    }
    const clip = parent.getBoundingClientRect();
    if (style.overflowX !== "visible") {
      left = Math.max(left, clip.left);
      right = Math.min(right, clip.right);
    }
    if (style.overflowY !== "visible") {
      top = Math.max(top, clip.top);
      bottom = Math.min(bottom, clip.bottom);
    }
  }
  return {
    left: Math.min(left, right),
    top: Math.min(top, bottom),
    right,
    bottom,
  };
}

function lineHeight(element: Element): number {
  const style = getComputedStyle(element);
  const line = Number.parseFloat(style.lineHeight);
  return Number.isNaN(line) ? Number.parseFloat(style.fontSize) * 1.2 : line;
}

// Line height of the text right under the point, or null off text.
function textLineAt(pointX: number, pointY: number): number | null {
  const position = document.caretPositionFromPoint(pointX, pointY);
  const node = position?.offsetNode;
  if (!node || node.nodeType !== Node.TEXT_NODE || !node.parentElement) {
    return null;
  }
  const range = document.createRange();
  range.selectNodeContents(node);
  const onText = [...range.getClientRects()].some(
    (rect) =>
      pointX >= rect.left &&
      pointX <= rect.right &&
      pointY >= rect.top &&
      pointY <= rect.bottom,
  );
  return onText ? lineHeight(node.parentElement) : null;
}
