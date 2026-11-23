import { create } from "zustand";

type ExecutionDrawerState = {
  selectedId: string | null;
  open: (id: string) => void;
  close: () => void;
};

/** Selected execution for the global detail drawer. Server data stays in TanStack Query. */
export const useExecutionDrawer = create<ExecutionDrawerState>((set) => ({
  selectedId: null,
  open: (selectedId) => set({ selectedId }),
  close: () => set({ selectedId: null }),
}));
