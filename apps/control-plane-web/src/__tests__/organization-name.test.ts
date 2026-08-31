import { describe, expect, it } from 'vitest';
import { organizationNameFromEmail } from '@/lib/organization-name';

describe('organizationNameFromEmail', () => {
  it('combines the username and domain', () => {
    expect(organizationNameFromEmail('Alex@Example.com')).toBe('alex-example-com');
  });

  it('normalizes punctuation safely', () => {
    expect(organizationNameFromEmail('first.last+dev@example.co.uk')).toBe(
      'first-last-dev-example-co-uk'
    );
  });
});
