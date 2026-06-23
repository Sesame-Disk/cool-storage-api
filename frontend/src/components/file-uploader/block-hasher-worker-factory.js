// Isolated worker factory. `import.meta.url` is required for webpack 5 to bundle
// the worker, but babel-jest (CommonJS) cannot parse it — so this lives in its
// own module that the orchestrator loads lazily (only on the real hashing path),
// keeping the orchestrator unit-testable with an injected hashFn.
export function createBlockHasherWorker() {
  return new Worker(new URL('./block-hasher.worker.js', import.meta.url));
}
