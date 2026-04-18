import { test, expect } from '@playwright/test';
import { register } from '../fixtures/users';

test('register creates account and shows dashboard', async ({ page }) => {
  await register(page, `reg-${Date.now()}@example.com`, 'TestPassword123');
  await expect(page).toHaveURL('/');
  await expect(page.locator('h1')).toContainText('Sync History');
});

test('unauthenticated access to dashboard redirects to login', async ({ page }) => {
  await page.goto('/');
  await expect(page).toHaveURL('/auth/login');
});

test('login with invalid credentials shows error', async ({ page }) => {
  await page.goto('/auth/login');
  await page.fill('input[name="email"]', 'nobody@example.com');
  await page.fill('input[name="password"]', 'wrongpassword');
  await page.click('button[type="submit"]');
  await expect(page.locator('.error')).toBeVisible();
  await expect(page).toHaveURL('/auth/login');
});

test('logout clears session', async ({ page }) => {
  await register(page, `logout-${Date.now()}@example.com`, 'TestPassword123');
  await page.click('button[type="submit"]'); // Logout button in Nav
  await expect(page).toHaveURL('/auth/login');
  await page.goto('/');
  await expect(page).toHaveURL('/auth/login');
});

test('register with short password shows error', async ({ page }) => {
  await page.goto('/auth/register');
  await page.fill('input[name="email"]', `short-${Date.now()}@example.com`);
  await page.fill('input[name="password"]', 'short');
  await page.click('button[type="submit"]');
  await expect(page.locator('.error')).toBeVisible();
});
