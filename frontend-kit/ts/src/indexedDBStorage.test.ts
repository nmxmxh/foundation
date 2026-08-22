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

const connectionClosingError = () => {
  const error = new Error("The database connection is closing.");
  error.name = "InvalidStateError";
  return error;
};

class FakeDatabase {
  readonly values = new Map<string, string>();
  readonly objectStoreNames = {
    contains: (name: string) => this.storeNames.has(name),
  };
  onversionchange?: () => void;
  onclose?: () => void;
  // When true, the next transaction fails like a browser-closed connection,
  // then heals to simulate reconnect-over-fresh-handle recovery.
  closingOnce = false;
  private readonly storeNames = new Set<string>();

  createObjectStore(name: string): void {
    this.storeNames.add(name);
  }

  evict(): void {
    // Browsers fire onclose when they kill a connection unilaterally.
    this.onclose?.();
  }

  transaction(storeName: string): IDBTransaction {
    if (this.closingOnce) {
      this.closingOnce = false;
      throw connectionClosingError();
    }
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

type OpenFailureMode = "none" | "once" | "always";

const installFakeIndexedDB = ({
  openFailures = "none",
}: { openFailures?: OpenFailureMode } = {}) => {
  const databases = new Map<string, FakeDatabase>();
  let opensSeen = 0;

  const fakeIndexedDB = {
    open(name: string): IDBOpenDBRequest {
      const request: FakeRequest<FakeDatabase> = { error: null };
      queueMicrotask(() => {
        opensSeen += 1;
        if (openFailures === "always" || (openFailures === "once" && opensSeen === 1)) {
          request.error = connectionClosingError();
          request.onerror?.();
          return;
        }
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

  return {
    databaseNamed: (name: string) => databases.get(name),
  };
};

let currentInstall: ReturnType<typeof installFakeIndexedDB> | null = null;

const install = (options?: Parameters<typeof installFakeIndexedDB>[0]) => {
  currentInstall = installFakeIndexedDB(options);
  return currentInstall;
};

afterEach(() => {
  Reflect.deleteProperty(globalThis, "indexedDB");
  currentInstall = null;
});

describe("createIndexedDBStorage", () => {
  it("stores, removes, and batch-removes records through IndexedDB", async () => {
    install();
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

  it("reopens over a fresh connection after a forced close", async () => {
    const harness = install();
    const storage = createIndexedDBStorage({ dbName: "frontend-kit-test" });

    await storage.setItem("tenant-a", "snapshot-a");
    // Simulate browser eviction: unilateral close, no versionchange.
    harness.databaseNamed("frontend-kit-test")?.evict();

    await storage.setItem("tenant-b", "snapshot-b");
    await expect(storage.getItem("tenant-a")).resolves.toBe("snapshot-a");
    await expect(storage.getItem("tenant-b")).resolves.toBe("snapshot-b");
  });

  it("retries once over a fresh connection when an operation hits a closing handle", async () => {
    const harness = install();
    const storage = createIndexedDBStorage({ dbName: "frontend-kit-test" });

    await storage.setItem("tenant-a", "snapshot-a");
    // Next transaction runs into a stale/closing connection, then heals.
    harness.databaseNamed("frontend-kit-test")!.closingOnce = true;

    await expect(storage.getItem("tenant-a")).resolves.toBe("snapshot-a");
  });

  it("does not let a failed open poison future operations", async () => {
    install({ openFailures: "once" });
    const storage = createIndexedDBStorage({ dbName: "frontend-kit-test" });

    // First open fails; reads fail soft instead of hanging or rejecting.
    await expect(storage.getItem("tenant-a")).resolves.toBeNull();

    // The poisoned-promise fix lets the next call open a fresh connection.
    await storage.setItem("tenant-a", "snapshot-a");
    await expect(storage.getItem("tenant-a")).resolves.toBe("snapshot-a");
  });

  it("fails soft without rejecting under persistent open failures", async () => {
    install({ openFailures: "always" });
    const storage = createIndexedDBStorage({ dbName: "frontend-kit-test" });

    await expect(storage.getItem("tenant-a")).resolves.toBeNull();
    await expect(storage.setItem("tenant-a", "snapshot-a")).resolves.toBeUndefined();
    await expect(storage.removeItem("tenant-a")).resolves.toBeUndefined();
    await expect(storage.removeItems(["tenant-a"])).resolves.toBeUndefined();
  });
});
