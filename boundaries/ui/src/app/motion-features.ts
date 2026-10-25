// Loaded after the first render so the animation engine stays out of the
// entry chunk; domMax adds layout animations on top of the basic ones.
export { domMax as default } from "motion/react";
