import "@testing-library/jest-dom";

// xterm.js requires matchMedia for DPR detection. jsdom doesn't provide it.
Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
});

// xterm.js also needs a minimal devicePixelContext.
Object.defineProperty(window, "devicePixelRatio", {
  writable: true,
  value: 1,
});

// Terminal.tsx uses ResizeObserver.
class ResizeObserverMock {
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
}
vi.stubGlobal("ResizeObserver", ResizeObserverMock);

// SSEContext reads localStorage for the auth token.
const localStorageStore: Record<string, string> = { bloxos_token: "test-token" };
Object.defineProperty(window, "localStorage", {
  writable: true,
  value: {
    getItem: vi.fn((key: string) => localStorageStore[key] ?? null),
    setItem: vi.fn((key: string, value: string) => {
      localStorageStore[key] = value;
    }),
    removeItem: vi.fn((key: string) => {
      delete localStorageStore[key];
    }),
    clear: vi.fn(() => {
      for (const key of Object.keys(localStorageStore)) {
        delete localStorageStore[key];
      }
    }),
  },
});
