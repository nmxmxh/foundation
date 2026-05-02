export interface StoreResetHandle {
  key: string;
  order: number;
  reset: () => void;
}

export interface StoreResetRegistry {
  register(handle: StoreResetHandle): () => void;
  run(): void;
  clear(): void;
  keys(): string[];
}

export const createStoreResetRegistry = (): StoreResetRegistry => {
  const handles = new Map<string, StoreResetHandle>();

  return {
    register: (handle) => {
      handles.set(handle.key, handle);
      return () => {
        const current = handles.get(handle.key);
        if (current === handle) {
          handles.delete(handle.key);
        }
      };
    },
    run: () => {
      Array.from(handles.values())
        .sort((left, right) => left.order - right.order)
        .forEach((handle) => handle.reset());
    },
    clear: () => handles.clear(),
    keys: () => Array.from(handles.keys()),
  };
};

export const createPersistenceClearer = (
  keys: readonly string[],
  storage: Storage | undefined = typeof localStorage !== "undefined" ? localStorage : undefined,
  asyncStorage?: { removeItems(keys: readonly string[]): Promise<void> }
) => async (): Promise<void> => {
  storage && keys.forEach((key) => storage.removeItem(key));
  await asyncStorage?.removeItems(keys);
};
