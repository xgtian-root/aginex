import type { Metadata, Viewport } from "next";
import { NextIntlClientProvider } from "next-intl";
import { getLocale, getTranslations } from "next-intl/server";
import { Toaster } from "sonner";
import { Providers } from "@/components/providers";
import { getLocaleDirection } from "@/i18n/config";
import "./globals.css";

export async function generateMetadata(): Promise<Metadata> {
  const t = await getTranslations("Metadata");
  return {
    title: {
      default: t("defaultTitle"),
      template: t("titleTemplate"),
    },
    description: t("description"),
  };
}

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  viewportFit: "cover",
  colorScheme: "light dark",
};

export default async function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const locale = await getLocale();
  const direction = getLocaleDirection(locale);

  return (
    <html dir={direction} lang={locale}>
      <body>
        <NextIntlClientProvider>
          <Providers>
            {children}
            <Toaster
              position={direction === "rtl" ? "bottom-left" : "bottom-right"}
              richColors
            />
          </Providers>
        </NextIntlClientProvider>
      </body>
    </html>
  );
}
