import {
  CloseButton,
  Dialog,
  DialogPanel,
  DialogTitle,
} from "@headlessui/react";
import { MotionConfig, m } from "motion/react";
import type { ReactNode } from "react";

export type ExpandedCardProps = Readonly<{
  // The card it grows out of carries the same layoutId; closing folds it
  // back there, wherever that card has moved in the meantime.
  layoutId: string | null;
  onClose: () => void;
  title: string;
  closeLabel: string;
  children: ReactNode;
}>;

const grow = { type: "spring", bounce: 0.15, duration: 0.4 } as const;

// A card opened over the screen, as in the App Store "Today" tab: the card
// itself grows into the dialog and its content fades in once it has room.
// Escape, the backdrop and the close button dismiss it, and focus returns to
// where it was. Closing unmounts it at once: the card takes over the shared
// layoutId and folds back from the dialog's last frame.
export function ExpandedCard({
  layoutId,
  onClose,
  title,
  closeLabel,
  children,
}: ExpandedCardProps) {
  return layoutId ? (
    <Dialog className="nomnom-expanded-root" onClose={onClose} open static>
      <m.div
        animate={{ opacity: 1 }}
        aria-hidden="true"
        className="nomnom-expanded-backdrop"
        initial={{ opacity: 0 }}
      />
      <div className="nomnom-expanded-frame">
        {/* DialogPanel owns `transition` itself, so the spring comes from here. */}
        <MotionConfig transition={grow}>
          <DialogPanel
            as={m.div}
            className="nomnom-expanded"
            layoutId={layoutId}
            style={{ borderRadius: "var(--radius-sheet)" }}
          >
            <m.div
              animate={{ opacity: 1, transition: { delay: 0.15 } }}
              className="nomnom-expanded-content"
              initial={{ opacity: 0 }}
            >
              <header className="nomnom-expanded-header">
                <DialogTitle className="nomnom-expanded-title">
                  {title}
                </DialogTitle>
                <CloseButton
                  aria-label={closeLabel}
                  className="nomnom-expanded-close"
                >
                  <span aria-hidden="true">×</span>
                </CloseButton>
              </header>
              <div className="nomnom-expanded-body">{children}</div>
            </m.div>
          </DialogPanel>
        </MotionConfig>
      </div>
    </Dialog>
  ) : null;
}
