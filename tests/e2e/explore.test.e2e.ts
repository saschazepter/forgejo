// document is a global in evaluate, so it's safe to ignore here
// eslint playwright/no-conditional-in-test: 0

// @watch start
// templates/explore/**
// web_src/js/modules/fomantic/**
// @watch end

import {expect} from '@playwright/test';
import {test} from './utils_e2e.ts';
import {screenshot} from './shared/screenshots.ts';

test('Explore view taborder', async ({page}) => {
  await page.goto('/explore/repos');

  const l1 = page.locator('[href="https://forgejo.org"]');
  const l2 = page.locator('[href="/assets/licenses.txt"]');
  const l3 = page.locator('[href*="/stars"]').first();
  const l4 = page.locator('[href*="/forks"]').first();
  let res = 0;
  const exp = 15; // 0b1111 = four passing tests

  for (let i = 0; i < 150; i++) {
    await page.keyboard.press('Tab');
    if (await l1.evaluate((node) => document.activeElement === node)) {
      res |= 1;
      continue;
    }
    if (await l2.evaluate((node) => document.activeElement === node)) {
      res |= 1 << 1;
      continue;
    }
    if (await l3.evaluate((node) => document.activeElement === node)) {
      res |= 1 << 2;
      continue;
    }
    if (await l4.evaluate((node) => document.activeElement === node)) {
      res |= 1 << 3;
      continue;
    }
    if (res === exp) {
      break;
    }
  }
  expect(res).toBe(exp);
  await screenshot(page);
});
