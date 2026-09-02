import type { NextConfig } from 'next';

const nextConfig: NextConfig = {
  agentRules: false,
  allowedDevOrigins: ['app.desktops.orchael.dev'],
  output: 'standalone',
  poweredByHeader: false,
  // Type checking runs explicitly before the build. This works around a
  // Next.js 16.3 CLI output parsing bug while preserving a hard type gate.
  typescript: { ignoreBuildErrors: true }
};

export default nextConfig;
