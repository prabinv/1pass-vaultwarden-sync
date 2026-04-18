import { test, expect } from '@playwright/test';
import { register, saveCredentials } from '../fixtures/users';

test('trigger sync creates job and redirects to job detail', async ({ page }) => {
  await register(page, `sync-${Date.now()}@example.com`, 'TestPassword123');
  await saveCredentials(page);

  await page.goto('/');
  await page.click('form[action="/sync/trigger"] button');

  await expect(page).toHaveURL(/\/sync\/jobs\/.+/);
  await expect(page.locator('h1')).toContainText('Sync Job');
  await expect(page.locator('#job-status')).toBeVisible();
});

test('job detail page shows status', async ({ page }) => {
  await register(page, `status-${Date.now()}@example.com`, 'TestPassword123');
  await saveCredentials(page);

  await page.goto('/');
  await page.click('form[action="/sync/trigger"] button');
  await page.waitForURL(/\/sync\/jobs\/.+/);

  await expect(page.locator('p').filter({ hasText: 'Status:' })).toBeVisible();
});

test('SSE stream endpoint returns text/event-stream', async ({ page }) => {
  await register(page, `sse-${Date.now()}@example.com`, 'TestPassword123');
  await saveCredentials(page);

  await page.goto('/');
  await page.click('form[action="/sync/trigger"] button');
  await page.waitForURL(/\/sync\/jobs\/(.+)/);

  const jobID = page.url().split('/').pop()!;
  const response = await page.request.get(`/sync/jobs/${jobID}/stream`, {
    headers: { Accept: 'text/event-stream' },
    timeout: 5000,
  });
  expect(response.headers()['content-type']).toContain('text/event-stream');
});

test('triggered job appears in dashboard history', async ({ page }) => {
  await register(page, `history-${Date.now()}@example.com`, 'TestPassword123');
  await saveCredentials(page);

  await page.goto('/');
  await page.click('form[action="/sync/trigger"] button');
  await page.waitForURL(/\/sync\/jobs\/.+/);

  await page.goto('/');
  await expect(page.locator('table')).toBeVisible();
  await expect(page.locator('td a')).toBeVisible();
});
