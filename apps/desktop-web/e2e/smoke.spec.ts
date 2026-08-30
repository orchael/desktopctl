import { test, expect } from '@playwright/test';

// All assertions are derived from MOCK_DATA in server/desktop.ts.

test.describe('desktop-web smoke', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
  });

  test('page title and header are visible', async ({ page }) => {
    await expect(page).toHaveTitle('AI Desktop');
    await expect(
      page.getByRole('heading', { name: 'AI Desktop' })
    ).toBeVisible();
    await expect(
      page.getByText('Local desktop status dashboard')
    ).toBeVisible();
  });

  test('identity section shows mock values', async ({ page }) => {
    await expect(page.getByText('mock-desktop')).toBeVisible();
    await expect(page.getByText('localhost')).toBeVisible();
    await expect(page.getByText('mock-owner')).toBeVisible();
    await expect(page.getByText('dev')).toBeVisible();
  });

  test('repositories section shows mock repo', async ({ page }) => {
    await expect(page.getByText('mock-repo')).toBeVisible();
  });

  test('services section shows all mock services with status', async ({
    page
  }) => {
    await expect(page.getByText('docker')).toBeVisible();
    await expect(page.getByText('bridgectl')).toBeVisible();
    await expect(page.getByText('novnc-desktop')).toBeVisible();

    // All three mock services are active
    const activeLabels = page.getByText('active');
    await expect(activeLabels).toHaveCount(3);
  });

  test('service versions are displayed', async ({ page }) => {
    // Mock data sets docker to 27.0.0 and bridgectl to 1.0.1
    await expect(page.getByText('v27.0.0')).toBeVisible();
    await expect(page.getByText('v1.0.1')).toBeVisible();
  });

  test('footer shows desktop-web version', async ({ page }) => {
    await expect(page.getByText('desktop-web v0.0.0-mock')).toBeVisible();
  });
});
