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

/**
 * Browsers close IndexedDB connections without asking: eviction,
 * backgrounding, or another tab winning a version upgrade. Those failures
 * surface as InvalidStateError ("connection is closing") storms and are
 * recoverable over a fresh connection. Everything else fails soft.
 */
const isRecoverableConnectionError = (error: unknown): boolean => {
  if (!error) return false;
  const name = typeof error === "object" ? String((error as { name?: unknown }).name ?? "") : "";
  const message = error instanceof Error ? error.message : String(error);
  return (
    name === "InvalidStateError" ||
    /connection is closing|database connection is closing/i.test(message)
  );
};

type Attempt<T> = { ok: true; value: T } | { ok: false; error: unknown };

export const createIndexedDBStorage = ({
  dbName,
  storeName = "app-store",
  version = 1,
  onError = defaultOnError,
}: IndexedDBStorageOptions): IndexedDBStorage => {
  let db: IDBDatabase | null = null;
  let dbPromise: Promise<IDBDatabase> | null = null;

  const isAvailable = () => typeof indexedDB !== "undefined";

  // Stale handles must never survive a close: every later operation would
  // fail forever instead of reopening.
  const dropHandle = () => {
    db = null;
    dbPromise = null;
  };

  const open = async (): Promise<IDBDatabase> => {
    if (!isAvailable()) {
      throw new Error("IndexedDB unavailable");
    }
    if (db) return db;
    if (dbPromise) return dbPromise;

    const request = indexedDB.open(dbName, version);

    dbPromise = new Promise((resolve, reject) => {
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
          dropHandle();
        };
        // Forced closes (eviction) fire onclose, not onversionchange.
        db.onclose = () => {
          dropHandle();
        };
        resolve(db);
      };

      request.onerror = () => reject(request.error);
      request.onblocked = () => reject(new Error(`IndexedDB open blocked for ${dbName}`));
    });

    // A failed open must not poison future attempts: clear the cached
    // promise so the next call opens a fresh connection.
    void dbPromise.catch(dropHandle);

    return dbPromise;
  };

  const attempt = async <T>(
    mode: IDBTransactionMode,
    operation: (store: IDBObjectStore) => IDBRequest | void,
    resolved: T
  ): Promise<Attempt<T>> => {
    try {
      const activeDB = await open();
      const value = await new Promise<T>((resolve, reject) => {
        const transaction = activeDB.transaction(storeName, mode);
        const store = transaction.objectStore(storeName);
        const request = operation(store);

        transaction.oncomplete = () => {
          if (request) {
            resolve((request.result ?? resolved) as T);
          } else {
            resolve(resolved);
          }
        };
        transaction.onerror = () => reject(transaction.error);
        transaction.onabort = () => reject(transaction.error);
        if (request) {
          request.onerror = () => reject(request.error);
        }
      });
      return { ok: true, value };
    } catch (error) {
      return { ok: false, error };
    }
  };

  // Every promise resolves: persist bursts can never emit unhandled
  // rejections, and reads fail soft to the caller's fallback.
  const run = async <T>(
    mode: IDBTransactionMode,
    operation: (store: IDBObjectStore) => IDBRequest | void,
    fallback: T
  ): Promise<T> => {
    if (!isAvailable()) return fallback;

    let result = await attempt<T>(mode, operation, fallback);
    // Recoverable connection failures get exactly one retry over a fresh
    // connection; the retry path also covers connections dropped between
    // open() and transaction().
    if (!result.ok && isRecoverableConnectionError(result.error)) {
      dropHandle();
      result = await attempt<T>(mode, operation, fallback);
    }
    if (!result.ok) {
      onError(mode, result.error);
      return fallback;
    }
    return result.value;
  };

  return {
    getItem: (name) => run<string | null>("readonly", (store) => store.get(name), null),
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
      dropHandle();
    },
  };
};
