import { DesktopDetails } from '@/components/desktop-details';

export default async function DesktopPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <DesktopDetails id={id} />;
}
