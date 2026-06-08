import { afterEach, describe, expect, it } from "vitest";

import { createIndexedDBStorage } from "./indexedDBStorage";

type FakeRequest<T = unknown> = {
  error: Error | null;
  result?: T;
  onblocked?: () => void;
  onerror?: () => void;
  onsuccess?: () => void;
  onupgradeneeded?: () => void;
};

type FakeTransaction = {
  error: Error | null;
  onabort?: () => void;
  oncomplete?: () => void;
  onerror?: () => void;
  objectStore(name: string): FakeObjectStore;
};

class FakeObjectStore {
  constructor(
    private readonly values: Map<string, string>,
    private readonly transaction: FakeTransaction
  ) {}

  get(name: string): IDBRequest {
    const request: FakeRequest<string | null> = { error: null };
    queueMicrotask(() => {
      request.result = this.values.get(name) ?? null;
      request.onsuccess?.();
      this.transaction.oncomplete?.();
    });
    return request as IDBRequest;
  }

  put(value: string, name: string): IDBRequest {
    const request: FakeRequest<undefined> = { error: null };
    queueMicrotask(() => {
      this.values.set(name, value);
      request.onsuccess?.();
      this.transaction.oncomplete?.();
    });
    return request as IDBRequest;
  }

  delete(name: string): IDBRequest {
    const request: FakeRequest<undefined> = { error: null };
    queueMicrotask(() => {
      this.values.delete(name);
      request.onsuccess?.();
      this.transaction.oncomplete?.();
    });
    return request as IDBRequest;
  }
}

class FakeDatabase {
  readonly values = new Map<string, string>();
  readonly objectStoreNames = {
    contains: (name: string) => this.storeNames.has(name),
  };
  onversionchange?: () => void;
  private readonly storeNames = new Set<string>();

  createObjectStore(name: string): void {
    this.storeNames.add(name);
  }

  transaction(storeName: string): IDBTransaction {
    const transaction: FakeTransaction = {
      error: null,
      objectStore: () => new FakeObjectStore(this.values, transaction),
    };
    this.storeNames.add(storeName);
    return transaction as IDBTransaction;
  }

  close(): void {
    // Real IDBDatabase.close does not emit versionchange; it only releases the handle.
  }
}

const installFakeIndexedDB = () => {
  const databases = new Map<string, FakeDatabase>();
  const fakeIndexedDB = {
    open(name: string): IDBOpenDBRequest {
      const request: FakeRequest<FakeDatabase> = { error: null };
      queueMicrotask(() => {
        const database = databases.get(name) ?? new FakeDatabase();
        databases.set(name, database);
        request.result = database;
        request.onupgradeneeded?.();
        request.onsuccess?.();
      });
      return request as IDBOpenDBRequest;
    },
  };
  Object.defineProperty(globalThis, "indexedDB", {
    configurable: true,
    value: fakeIndexedDB,
  });
};

afterEach(() => {
  Reflect.deleteProperty(globalThis, "indexedDB");
});

describe("createIndexedDBStorage", () => {
  it("stores, removes, and batch-removes records through IndexedDB", async () => {
    installFakeIndexedDB();
    const storage = createIndexedDBStorage({ dbName: "frontend-kit-test" });

    await storage.setItem("tenant-a", "snapshot-a");
    await storage.setItem("tenant-b", "snapshot-b");

    await expect(storage.getItem("tenant-a")).resolves.toBe("snapshot-a");
    await storage.removeItem("tenant-a");
    await expect(storage.getItem("tenant-a")).resolves.toBeNull();

    await storage.removeItems(["tenant-b"]);
    await expect(storage.getItem("tenant-b")).resolves.toBeNull();
    storage.close();
  });

  it("fails closed when IndexedDB is unavailable", async () => {
    const storage = createIndexedDBStorage({ dbName: "missing-indexeddb" });

    await expect(storage.getItem("tenant-a")).resolves.toBeNull();
    await expect(storage.setItem("tenant-a", "snapshot-a")).resolves.toBeUndefined();
  });
});
