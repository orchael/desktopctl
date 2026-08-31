export function organizationNameFromEmail(email: string): string {
  return email
    .trim()
    .toLowerCase()
    .replace('@', '-')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '')
    .slice(0, 80);
}
