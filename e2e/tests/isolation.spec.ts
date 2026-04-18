import { test, expect } from '@playwright/test';
import { register, saveCredentials } from '../fixtures/users';

test('user A cannot access user B job detail', async ({ browser }) => {
  const ts = Date.now();

  const ctxA = await browser.newContext();
  const ctxB = await browser.newContext();
  const pageA = await ctxA.newPage();
  const pageB = await ctxB.newPage();

  try {
    // User A registers, saves credentials, triggers a sync
    await register(pageA, `userA-${ts}@example.com`, 'TestPassword123');
    await saveCredentials(pageA);
    await pageA.goto('/');
    await pageA.click('form[action="/sync/trigger"] button');
    await pageA.waitForURL(/\/sync\/jobs\/.+/);
    const jobID = pageA.url().split('/').pop()!;

    // User B registers separately
    await register(pageB, `userB-${ts}@example.com`, 'TestPassword123');

    // User B tries to access User A's job — must get 404
    const response = await pageB.request.get(`/sync/jobs/${jobID}`);
    expect(response.status()).toBe(404);
  } finally {
    await ctxA.close();
    await ctxB.close();
  }
});

test('user A cannot access user B credentials via direct GET', async ({ browser }) => {
  const ts = Date.now();

  const ctxA = await browser.newContext();
  const ctxB = await browser.newContext();
  const pageA = await ctxA.newPage();
  const pageB = await ctxB.newPage();

  try {
    await register(pageA, `credA-${ts}@example.com`, 'TestPassword123');
    await register(pageB, `credB-${ts}@example.com`, 'TestPassword123');

    await saveCredentials(pageA);

    // User B's credentials page shows no credentials (not user A's)
    await pageB.goto('/credentials');
    const token = await pageB.inputValue('input[name="op_token"]');
    expect(token).toBe(''); // User B should see empty form
  } finally {
    await ctxA.close();
    await ctxB.close();
  }
});
