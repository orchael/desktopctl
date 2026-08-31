import type { Metadata } from 'next';
import './globals.css';

export const metadata: Metadata = {
  title: 'ai-desktops Control Plane',
  description: 'Organization-scoped cloud desktop operations'
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
