import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Self-contained server for the container image (Dockerfile.dashboard).
  output: "standalone",
  // Pin the project root so file tracing does not climb to a lockfile in a
  // parent directory and nest the standalone output under that path.
  turbopack: { root: process.cwd() },
  // The dashboard uses plain <img> for the one user-provided image, so no
  // server-side optimizer (sharp) is ever required; this keeps the container
  // image free of platform-specific native modules.
  images: { unoptimized: true },
};

export default nextConfig;
