import type { Metadata } from 'next';
import './globals.css';

export const metadata: Metadata = {
  title: 'desktopctl Companion App',
  description: 'Private browser interface for a desktopctl fleet'
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
