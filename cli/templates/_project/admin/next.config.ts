import path from "node:path";
import type { NextConfig } from "next";
import createNextIntlPlugin from "next-intl/plugin";

const withNextIntl = createNextIntlPlugin("./i18n/request.ts");

const nextConfig: NextConfig = {
  output: "standalone",
  outputFileTracingRoot: path.resolve(__dirname, ".."),
  reactStrictMode: true,
  poweredByHeader: false,
  async rewrites() {
    if (
      process.env.NODE_ENV !== "development" ||
      process.env.NEXT_PUBLIC_API_URL?.trim()
    ) {
      return [];
    }
    const target = (
      process.env.AGINEX_API_INTERNAL_URL ?? "http://127.0.0.1:8080"
    )
      .replace(/\/api\/v1\/?$/, "")
      .replace(/\/$/, "");
    return [
      {
        source: "/api/:path*",
        destination: `${target}/api/:path*`,
      },
    ];
  },
};

export default withNextIntl(nextConfig);
