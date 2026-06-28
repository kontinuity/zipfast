type CacheEntry<T> = {
  value: T;
  expiresAt: number;
  timeout: NodeJS.Timeout;
};

export class TimedCache<K, V> {
  private cache = new Map<K, CacheEntry<V>>();
  private ttl: number;

  constructor(ttl = 60_000) {
    this.ttl = ttl;
  }

  set(key: K, value: V, ttl = this.ttl): void {
    const expiresAt = Date.now() + ttl;

    const existing = this.cache.get(key);
    if (existing) clearTimeout(existing.timeout);

    const timeout = setTimeout(() => this.cache.delete(key), ttl);
    timeout.unref?.();

    this.cache.set(key, { value, expiresAt, timeout });
  }

  get(key: K): V | undefined {
    const entry = this.cache.get(key);
    if (!entry) return undefined;

    if (entry.expiresAt <= Date.now()) {
      this.delete(key);
      return undefined;
    }

    return entry.value;
  }

  has(key: K): boolean {
    return this.get(key) !== undefined;
  }

  delete(key: K): boolean {
    const entry = this.cache.get(key);
    if (entry) clearTimeout(entry.timeout);

    return this.cache.delete(key);
  }
}
