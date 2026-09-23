import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "namma-transit",
  description:
    "Group travel cost and time estimator for Bengaluru public transport",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body className="bg-white text-neutral-900 antialiased dark:bg-neutral-950 dark:text-neutral-100">
        {children}
      </body>
    </html>
  );
}
