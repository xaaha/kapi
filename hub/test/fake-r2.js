export class FakeR2 {
  constructor({ pageSize = 1000 } = {}) {
    this.objects = new Map();
    this.pageSize = pageSize;
    this.clock = 0;
  }

  async put(key, value, options = {}) {
    const bytes = typeof value === "string" ? new TextEncoder().encode(value) : new Uint8Array(value);
    this.clock++;
    this.objects.set(key, {
      key,
      bytes,
      size: bytes.byteLength,
      uploaded: new Date(this.clock * 1000),
      httpMetadata: { ...options.httpMetadata },
      customMetadata: { ...options.customMetadata },
    });
    return this.#meta(key);
  }

  async head(key) {
    return this.objects.has(key) ? this.#meta(key) : null;
  }

  async get(key) {
    const obj = this.objects.get(key);
    if (!obj) return null;
    const bytes = obj.bytes.slice();
    return { ...this.#meta(key), body: bytes, text: async () => new TextDecoder().decode(bytes) };
  }

  async list({ prefix = "", cursor, include = [] } = {}) {
    const keys = [...this.objects.keys()].filter((k) => k.startsWith(prefix)).sort();
    const start = cursor ? Number(cursor) : 0;
    const slice = keys.slice(start, start + this.pageSize);
    const truncated = start + this.pageSize < keys.length;
    return {
      objects: slice.map((k) => {
        const meta = this.#meta(k);
        if (!include.includes("customMetadata")) delete meta.customMetadata;
        return meta;
      }),
      truncated,
      cursor: truncated ? String(start + this.pageSize) : undefined,
    };
  }

  #meta(key) {
    const { bytes: _bytes, ...meta } = this.objects.get(key);
    return { ...meta, customMetadata: { ...meta.customMetadata } };
  }
}
