import { Page } from '@playwright/test';

export async function register(page: Page, email: string, password: string): Promise<void> {
  await page.goto('/auth/register');
  await page.fill('input[name="email"]', email);
  await page.fill('input[name="password"]', password);
  await page.click('button[type="submit"]');
  await page.waitForURL('/');
}

export async function login(page: Page, email: string, password: string): Promise<void> {
  await page.goto('/auth/login');
  await page.fill('input[name="email"]', email);
  await page.fill('input[name="password"]', password);
  await page.click('button[type="submit"]');
  await page.waitForURL('/');
}

export async function saveCredentials(page: Page): Promise<void> {
  await page.goto('/credentials');
  await page.fill('input[name="op_token"]',           'ops_fake_token_for_testing');
  await page.fill('input[name="vw_url"]',             'https://vault.example.com');
  await page.fill('input[name="vw_client_id"]',       'fake-client-id');
  await page.fill('input[name="vw_client_secret"]',   'fake-client-secret');
  await page.fill('input[name="vw_master_password"]', 'fake-master-password');
  await page.click('button[type="submit"]');
  await page.waitForSelector('.success');
}
