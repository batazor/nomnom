import {
  Button,
  CloseButton,
  Dialog,
  DialogBackdrop,
  DialogPanel,
  DialogTitle,
} from "@headlessui/react";
import type { ReactNode } from "react";

export type SidePanelProps = Readonly<{
  isOpen: boolean;
  onClose: () => void;
  title: string;
  closeLabel: string;
  children: ReactNode;
}>;

// Panel that slides in from the side over the screen; Escape, the backdrop and
// the close button dismiss it, and focus returns to where it was.
export function SidePanel({
  isOpen,
  onClose,
  title,
  closeLabel,
  children,
}: SidePanelProps) {
  return (
    <Dialog className="nomnom-side-panel-root" onClose={onClose} open={isOpen}>
      <DialogBackdrop className="nomnom-side-panel-backdrop" transition />
      <DialogPanel className="nomnom-side-panel" transition>
        <header className="nomnom-side-panel-header">
          <DialogTitle className="nomnom-side-panel-title">{title}</DialogTitle>
          <CloseButton
            aria-label={closeLabel}
            as={Button}
            className="nomnom-side-panel-close"
          >
            <span aria-hidden="true">×</span>
          </CloseButton>
        </header>
        <div className="nomnom-side-panel-body">{children}</div>
      </DialogPanel>
    </Dialog>
  );
}
