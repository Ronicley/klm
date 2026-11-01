import { expect, it } from 'vitest';
import { releaseRunNowAttempt } from './runNowAttempt';

it('releases a fresh attempt when local persistence failed before dispatch', () => {
  expect(releaseRunNowAttempt(false, false)).toBe(true);
});

it('releases known rejection of a fresh request so the next explicit action can use a new revision/ID', () => {
  for (const status of [400, 401, 403, 404, 409]) expect(releaseRunNowAttempt(false, true, status)).toBe(true);
});

it('releases an exact revision conflict even after uncertainty because the receipt lookup precedes that check', () => {
  expect(releaseRunNowAttempt(true, true, 409, 'Automation changed.')).toBe(true);
});

it('preserves uncertain outcomes and prior uncertain IDs when a later request never dispatched', () => {
  expect(releaseRunNowAttempt(true, false)).toBe(false);
  expect(releaseRunNowAttempt(false, true)).toBe(false);
  for (const status of [408, 429, 500, 502, 503]) expect(releaseRunNowAttempt(false, true, status)).toBe(false);
});

it('does not mistake auth/missing-entity/other conflicts for proof against an earlier accepted request', () => {
  for (const status of [400, 401, 403, 404, 409]) expect(releaseRunNowAttempt(true, true, status, 'Other failure.')).toBe(false);
  expect(releaseRunNowAttempt(true, true, 409, 'Project unavailable.')).toBe(false);
  expect(releaseRunNowAttempt(true, true, undefined, 'Automation changed.')).toBe(false);
});
