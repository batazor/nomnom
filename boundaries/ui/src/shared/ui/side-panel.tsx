import { Drawer } from "@astra-rn/react/drawer";
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
    <Drawer
      isOpen={isOpen}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <Drawer.Backdrop>
        <Drawer.Content className="nomnom-side-panel" placement="right">
          <Drawer.Dialog>
            <Drawer.Header>
              <Drawer.Heading>{title}</Drawer.Heading>
              <Drawer.CloseTrigger aria-label={closeLabel} />
            </Drawer.Header>
            <Drawer.Body>{children}</Drawer.Body>
          </Drawer.Dialog>
        </Drawer.Content>
      </Drawer.Backdrop>
    </Drawer>
  );
}
