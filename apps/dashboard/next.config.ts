import type { NextConfig } from 'next';

const nextConfig: NextConfig = {
  reactStrictMode: true,
  // Enable standalone output for Docker
  output: 'standalone',
  // Disable dev indicators
  devIndicators: false,
  // Customize webpack to suppress certain warnings
  webpack: (config, { dev }) => {
    if (dev) {
      // Suppress WebSocket and other non-critical warnings in dev
      config.infrastructureLogging = {
        level: 'error',
      };
    }
    return config;
  },
};

export default nextConfig;
