require('@testing-library/jest-dom');

// Polyfill TextEncoder/TextDecoder for jsdom
const { TextEncoder, TextDecoder } = require('util');
global.TextEncoder = TextEncoder;
global.TextDecoder = TextDecoder;

// Minimal ResizeObserver polyfill for jsdom (which does not implement it).
// This is deliberately inert: it stores the callback passed to the
// constructor and exposes no-op-safe observe/unobserve/disconnect methods,
// but never invokes the callback on its own (real ResizeObserver delivers
// entries asynchronously/batched after layout, never synchronously inside
// observe()). Individual test files that need to *drive* delivery capture
// the callback themselves (e.g. by wrapping global.ResizeObserver with a
// jest.fn()-based spy locally) and invoke it explicitly with fabricated
// ResizeObserverEntry-shaped objects.
class MockResizeObserver {
  constructor(callback) {
    this.callback = callback;
  }
  observe() {}
  unobserve() {}
  disconnect() {}
}
global.ResizeObserver = MockResizeObserver;

// jsdom does not implement window.matchMedia. Minimal polyfill so components
// that probe prefers-color-scheme (e.g. XtermTerminal's theme-sync effect)
// don't throw during mount.
if (typeof window !== 'undefined' && !window.matchMedia) {
  window.matchMedia = (query) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  });
}
