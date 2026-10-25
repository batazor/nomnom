import { AnimatePresence, m } from "motion/react";
import { useRef } from "react";

export type AnimatedNumberProps = Readonly<{
  // The number as it should read, already formatted for the locale.
  text: string;
  // Its value, only to tell whether it went up or down.
  value: number;
  className?: string;
}>;

const roll = {
  enter: (direction: number) => ({ y: `${direction * 70}%`, opacity: 0 }),
  shown: { y: "0%", opacity: 1 },
  leave: (direction: number) => ({ y: `${direction * -70}%`, opacity: 0 }),
};

// A number whose changed characters roll: up when it grows, down when it
// shrinks; the unchanged ones stay put. Screen readers get the plain text.
export function AnimatedNumber({
  text,
  value,
  className,
}: AnimatedNumberProps) {
  const previous = useRef(value);
  const direction = value >= previous.current ? 1 : -1;
  previous.current = value;
  const characters = [...text];

  return (
    <span className={["nomnom-number", className].filter(Boolean).join(" ")}>
      <span className="sr-only">{text}</span>
      <span aria-hidden="true" className="nomnom-number-digits">
        {characters.map((character, index) => {
          // Slots count from the right, so ones stay ones when the number
          // grows a digit. The old and the new character share the slot's
          // grid cell while one rolls out and the other in (popLayout would
          // do this with an injected <style> the CSP blocks).
          const slot = characters.length - index;
          return (
            <span className="nomnom-number-slot" key={slot}>
              <AnimatePresence custom={direction} initial={false}>
                <m.span
                  animate="shown"
                  custom={direction}
                  exit="leave"
                  initial="enter"
                  key={character}
                  transition={{ type: "spring", bounce: 0, duration: 0.35 }}
                  variants={roll}
                >
                  {character}
                </m.span>
              </AnimatePresence>
            </span>
          );
        })}
      </span>
    </span>
  );
}
