"use client";
import { Component, ReactNode } from "react";

interface Props {
  children: ReactNode;
}
interface State {
  hasError: boolean;
  error?: Error;
}

export class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props);
    this.state = { hasError: false };
  }

  static getDerivedStateFromError(error: Error) {
    return { hasError: true, error };
  }

  render() {
    if (this.state.hasError) {
      return (
        <div className="min-h-screen bg-blox-bg flex items-center justify-center">
          <div className="text-center space-y-4">
            <div className="text-blox-red text-lg font-semibold">
              Something went wrong
            </div>
            <div className="text-blox-muted text-xs max-w-md">
              {this.state.error?.message}
            </div>
            <button
              onClick={() => {
                this.setState({ hasError: false });
                window.location.reload();
              }}
              className="px-4 py-2 rounded bg-blox-blue/10 text-blox-blue text-xs border border-blox-blue/20 hover:bg-blox-blue/20"
            >
              Reload
            </button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}
