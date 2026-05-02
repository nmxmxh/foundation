export interface AsyncKeyValueStorage {
  getItem(name: string): Promise<string | null>;
  setItem(name: string, value: string): Promise<void>;
  removeItem(name: string): Promise<void>;
}

export interface IndexedDBStorageOptions {
  dbName: string;
  storeName?: string;
  version?: number;
  onError?: (operation: string, error: unknown) => void;
}

export interface IndexedDBStorage extends AsyncKeyValueStorage {
  removeItems(names: readonly string[]): Promise<void>;
  close(): void;
}

const defaultOnError = (operation: string, error: unknown) => {
  if (typeof console !== "undefined") {
    console.warn(`[frontend-kit:indexeddb] ${operation} failed`, error);
  }
};

export const createIndexedDBStorage = ({
  dbName,
  storeName = "app-store",
  version = 1,
  onError = defaultOnError,
}: IndexedDBStorageOptions): IndexedDBStorage => {
  let db: IDBDatabase | null = null;
  let dbPromise: Promise<IDBDatabase> | null = null;

  const isAvailable = () => typeof indexedDB !== "undefined";

  const open = async (): Promise<IDBDatabase> => {
    if (!isAvailable()) {
      throw new Error("IndexedDB unavailable");
    }
    if (db) return db;
    if (dbPromise) return dbPromise;

    dbPromise = new Promise((resolve, reject) => {
      const request = indexedDB.open(dbName, version);

      request.onupgradeneeded = () => {
        const next = request.result;
        if (!next.objectStoreNames.contains(storeName)) {
          next.createObjectStore(storeName);
        }
      };

      request.onsuccess = () => {
        db = request.result;
        db.onversionchange = () => {
          db?.close();
          db = null;
          dbPromise = null;
        };
        resolve(db);
      };

      request.onerror = () => reject(request.error);
      request.onblocked = () => reject(new Error(`IndexedDB open blocked for ${dbName}`));
    });

    return dbPromise;
  };

  const run = async <T>(
    mode: IDBTransactionMode,
    operation: (store: IDBObjectStore) => IDBRequest | void,
    fallback: T
  ): Promise<T> => {
    if (!isAvailable()) return fallback;

    try {
      const activeDB = await open();
      return await new Promise<T>((resolve, reject) => {
        const transaction = activeDB.transaction(storeName, mode);
        const store = transaction.objectStore(storeName);
        const request = operation(store);

        transaction.oncomplete = () => {
          if (request) {
            resolve((request.result ?? fallback) as T);
          } else {
            resolve(fallback);
          }
        };
        transaction.onerror = () => reject(transaction.error);
        transaction.onabort = () => reject(transaction.error);
        if (request) {
          request.onerror = () => reject(request.error);
        }
      });
    } catch (error) {
      onError(mode, error);
      return fallback;
    }
  };

  return {
    getItem: (name) => run("readonly", (store) => store.get(name), null),
    setItem: async (name, value) => {
      await run("readwrite", (store) => store.put(value, name), undefined);
    },
    removeItem: async (name) => {
      await run("readwrite", (store) => store.delete(name), undefined);
    },
    removeItems: async (names) => {
      if (names.length === 0) return;
      await run(
        "readwrite",
        (store) => {
          names.forEach((name) => store.delete(name));
        },
        undefined
      );
    },
    close: () => {
      db?.close();
      db = null;
      dbPromise = null;
    },
  };
};
