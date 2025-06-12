// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// @watch start
// templates/user/auth/**
// templates/user/settings/**
// web_src/js/features/user-**
// @watch end

import {expect} from '@playwright/test';
import {test, save_visual, create_temp_user, login_user} from './utils_e2e.ts';

test('WebAuthn register & login flow', async ({browser, request}, workerInfo) => {
  test.skip(workerInfo.project.name !== 'chromium', 'Uses Chrome protocol');
  const {context, username} = await create_temp_user(browser, workerInfo, request);
  const page = await context.newPage();

  // Register a security key.
  let response = await page.goto('/user/settings/security');
  expect(response?.status()).toBe(200);

  // https://github.com/microsoft/playwright/issues/7276#issuecomment-1516768428
  const cdpSession = await page.context().newCDPSession(page);
  await cdpSession.send('WebAuthn.enable');
  await cdpSession.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      ctap2Version: 'ctap2_1',
      hasUserVerification: true,
      transport: 'usb',
      automaticPresenceSimulation: true,
      isUserVerified: true,
    },
  });

  await page.locator('input#nickname').fill('Testing Security Key');
  await save_visual(page);
  await page.getByText('Add security key').click();

  // Logout.
  await expect(async () => {
    await page.locator('div[aria-label="Profile and settings…"]').click();
    await page.getByText('Sign out').click();
  }).toPass();
  await page.waitForURL(`${workerInfo.project.use.baseURL}/`);

  // Login.
  response = await page.goto('/user/login');
  expect(response?.status()).toBe(200);

  await page.getByLabel('Username or email address').fill(username);
  await page.getByLabel('Password').fill('password');
  await page.getByRole('button', {name: 'Sign in'}).click();
  await page.waitForURL(`${workerInfo.project.use.baseURL}/user/webauthn`);
  await page.waitForURL(`${workerInfo.project.use.baseURL}/`);

  // Cleanup.
  response = await page.goto('/user/settings/security');
  expect(response?.status()).toBe(200);
  await page.getByRole('button', {name: 'Remove'}).click();
  await save_visual(page);
  await page.getByRole('button', {name: 'Yes'}).click();
  await page.waitForLoadState();

  // verify the user can login without a key
  await login_user(browser, workerInfo, username);
});
