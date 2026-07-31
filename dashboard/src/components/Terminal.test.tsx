import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, act } from "@testing-library/react";
import { Terminal } from "@/components/Terminal";

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock("@/contexts/ThemeContext", () => ({
  useTheme: () => ({ resolvedMode: "dark" }),
}));

vi.mock("@/lib/session", () => ({
  getHubWsBaseUrl: () => "ws://localhost:4000",
}));

// ---------------------------------------------------------------------------
// Test 2: Terminal WebSocket reconnect
// ---------------------------------------------------------------------------

describe("Terminal", () => {
  let container: HTMLDivElement;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });

  afterEach(() => {
    vi.useRealTimers();
    container.remove();
  });

  it("reconnects with exponential backoff after ws.onclose", async () => {
    const wsInstances: MockWS[] = [];

    // Minimal WebSocket mock that doesn't extend the real class (which has
    // read-only getters for url, readyState, etc.).
    class MockWS {
      static CONNECTING = 0;
      static OPEN = 1;
      static CLOSING = 2;
      static CLOSED = 3;

      url: string;
      readyState = MockWS.CONNECTING;
      binaryType: BinaryType = "arraybuffer";
      onopen: ((this: WebSocket, ev: Event) => any) | null = null;
      onmessage: ((this: WebSocket, ev: MessageEvent) => any) | null = null;
      onclose: ((this: WebSocket, ev: CloseEvent) => any) | null = null;
      onerror: ((this: WebSocket, ev: Event) => any) | null = null;
      send = vi.fn();
      close = vi.fn(() => {
        this.readyState = MockWS.CLOSED;
      });

      constructor(url: string) {
        this.url = url;
        wsInstances.push(this);
      }
    }

    vi.stubGlobal("WebSocket", MockWS);

    render(
      <Terminal sessionId="test-session" browserToken="test-token" />,
      { container }
    );

    // First connection attempt is in-flight.
    expect(wsInstances.length).toBe(1);
    const ws1 = wsInstances[0];

    // Simulate successful open.
    act(() => {
      ws1.readyState = MockWS.OPEN;
      ws1.onopen?.(new Event("open"));
    });

    await waitFor(() =>
      expect(screen.queryByText(/Connecting/i)).not.toBeInTheDocument()
    );

    // Simulate server-side close (e.g. hub restart).
    act(() => {
      ws1.readyState = MockWS.CLOSED;
      ws1.onclose?.(new CloseEvent("close"));
    });

    // After close, the component should schedule a reconnect.
    // First backoff = 3000ms (attempt 0, then increments to 1).
    act(() => {
      vi.advanceTimersByTime(3000);
    });

    await waitFor(() => expect(wsInstances.length).toBe(2));
    const ws2 = wsInstances[1];

    // Simulate second open — this resets the attempt counter.
    act(() => {
      ws2.readyState = MockWS.OPEN;
      ws2.onopen?.(new Event("open"));
    });

    // Second close → attempt counter was reset by onopen, so delay is 3000ms again.
    act(() => {
      ws2.readyState = MockWS.CLOSED;
      ws2.onclose?.(new CloseEvent("close"));
    });

    // Advance 3000ms — reconnect should fire because backoff reset to 3000ms.
    act(() => {
      vi.advanceTimersByTime(3000);
    });

    await waitFor(() => expect(wsInstances.length).toBe(3));

    // Third close without an intervening open — now attempt is 1, so delay = 6000ms.
    const ws3 = wsInstances[2];
    act(() => {
      ws3.readyState = MockWS.CLOSED;
      ws3.onclose?.(new CloseEvent("close"));
    });

    // 3000ms is not enough.
    act(() => {
      vi.advanceTimersByTime(3000);
    });
    expect(wsInstances.length).toBe(3);

    // Another 3000ms makes 6000ms total — now reconnect fires.
    act(() => {
      vi.advanceTimersByTime(3000);
    });

    await waitFor(() => expect(wsInstances.length).toBe(4));

    vi.unstubAllGlobals();
  });
});
