import { create } from 'zustand';

type FileNavStore = {
  ids: string[];
  current: string | null;

  setFiles: (fileIds: string[]) => void;
  setCurrent: (fileId: string | null) => void;
  clear: () => void;
  goPrev: () => void;
  goNext: () => void;
};

export const useFileNavStore = create<FileNavStore>()((set) => ({
  ids: [],
  current: null,

  setFiles: (fileIds) =>
    set((state) => {
      if (!state.current || fileIds.includes(state.current)) {
        return { ids: fileIds };
      }

      return {
        ids: fileIds,
        current: null,
      };
    }),

  setCurrent: (fileId) => set({ current: fileId }),

  clear: () => set({ ids: [], current: null }),

  goPrev: () =>
    set((state) => {
      if (!state.current) return state;

      const idx = state.ids.indexOf(state.current);
      if (idx <= 0) return state;

      return {
        current: state.ids[idx - 1],
      };
    }),

  goNext: () =>
    set((state) => {
      if (!state.current) return state;

      const idx = state.ids.indexOf(state.current);
      if (idx < 0 || idx >= state.ids.length - 1) return state;

      return {
        current: state.ids[idx + 1],
      };
    }),
}));
