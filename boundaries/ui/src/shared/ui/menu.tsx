// Menu primitives of Headless UI (ADR 38): the only entry to its Menu outside
// src/shared/ui. They keep focus, keyboard and ARIA of a menu button; the
// caller styles them by class, like the account menu.
export {
  Menu,
  MenuButton,
  MenuItem,
  MenuItems,
  MenuSeparator,
} from "@headlessui/react";
