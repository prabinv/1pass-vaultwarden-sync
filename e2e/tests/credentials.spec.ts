import { test, expect } from '@playwright/test';
import { register, saveCredentials } from '../fixtures/users';

test('save credentials shows success message', async ({ page }) => {
  await register(page, `creds-${Date.now()}@example.com`, 'TestPassword123');
  await saveCredentials(page);
  await expect(page.locator('.success')).toBeVisible();
});

test('credentials form pre-fills after saving', async ({ page }) => {
  await register(page, `prefill-${Date.now()}@example.com`, 'TestPassword123');
  await saveCredentials(page);
  // Navigate away and back
  await page.goto('/');
  await page.goto('/credentials');
  // URL field should be visible and prefilled
  const url = await page.inputValue('input[name="vw_url"]');
  expect(url).toBe('https://vault.example.com');
});

test('save with empty fields shows error', async ({ page }) => {
  await register(page, `empty-${Date.now()}@example.com`, 'TestPassword123');
  await page.goto('/credentials');
  // Submit form without filling fields — handler validates empty fields
  await page.fill('input[name="op_token"]',           ' ');
  await page.fill('input[name="vw_url"]',             ' ');
  await page.fill('input[name="vw_client_id"]',       ' ');
  await page.fill('input[name="vw_client_secret"]',   ' ');
  await page.fill('input[name="vw_master_password"]', ' ');
  await page.click('button[type="submit"]');
  await expect(page.locator('.error')).toBeVisible();
});
