import type { NextConfig } from 'next';

const nextConfig: NextConfig = {
  reactStrictMode: true,
  // Enable standalone output for Docker
  output: 'standalone',
};

export default nextConfig;
