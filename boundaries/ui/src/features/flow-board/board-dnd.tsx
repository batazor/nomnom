import {
  createContext,
  type DragEvent,
  type ReactNode,
  useContext,
  useMemo,
  useRef,
  useState,
} from "react";

// What can be carried across the board: the customer's cart to the kitchen,
// which places the order, or a kitchen order to a free courier of the pool,
// who gets an offer of it.
export type BoardCargo =
  | { kind: "cart"; submit: () => void }
  | { kind: "order"; orderId: string };

const mimeType = "application/x-nomnom-board";

type DragState = Readonly<{
  cargo: BoardCargo | null;
  start: (cargo: BoardCargo) => void;
  end: () => void;
}>;

const DragContext = createContext<DragState>({
  cargo: null,
  start: () => {},
  end: () => {},
});

export function BoardDragProvider({ children }: { children: ReactNode }) {
  const [cargo, setCargo] = useState<BoardCargo | null>(null);
  const value = useMemo(
    () => ({ cargo, start: setCargo, end: () => setCargo(null) }),
    [cargo],
  );
  return <DragContext.Provider value={value}>{children}</DragContext.Provider>;
}

// Props that make an element carry cargo. The payload itself stays in React:
// the data transfer only marks the drag as a board drag.
export function useDragSource(cargo: BoardCargo | null) {
  const { start, end } = useContext(DragContext);
  if (!cargo) return {};
  return {
    draggable: true,
    "data-dragging": undefined,
    onDragStart(event: DragEvent<HTMLElement>) {
      event.dataTransfer.effectAllowed = "move";
      event.dataTransfer.setData(mimeType, cargo.kind);
      start(cargo);
    },
    onDragEnd: end,
  } as const;
}

// Props of an element that accepts cargo of one kind while enabled. data-drop
// is "ready" while matching cargo is being dragged and "over" while it hovers
// here.
export function useDropTarget<K extends BoardCargo["kind"]>(
  kind: K,
  onDrop: (cargo: Extract<BoardCargo, { kind: K }>) => void,
  enabled = true,
) {
  const { cargo, end } = useContext(DragContext);
  const [over, setOver] = useState(false);
  // dragenter/dragleave fire for every child; the depth tells when the
  // pointer really left the element.
  const depth = useRef(0);
  const accepts = enabled && cargo?.kind === kind;

  return {
    "data-drop": accepts ? (over ? "over" : "ready") : undefined,
    onDragEnter(event: DragEvent<HTMLElement>) {
      if (!accepts) return;
      event.preventDefault();
      depth.current += 1;
      setOver(true);
    },
    onDragOver(event: DragEvent<HTMLElement>) {
      if (!accepts) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = "move";
    },
    onDragLeave() {
      if (!accepts) return;
      depth.current = Math.max(0, depth.current - 1);
      if (depth.current === 0) setOver(false);
    },
    onDrop(event: DragEvent<HTMLElement>) {
      if (!accepts || !cargo) return;
      event.preventDefault();
      depth.current = 0;
      setOver(false);
      end();
      onDrop(cargo as Extract<BoardCargo, { kind: K }>);
    },
  };
}
