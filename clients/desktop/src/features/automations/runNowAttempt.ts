// In runSQLAutomationNow the manual-operation lookup precedes the revision check.
// Its exact "Automation changed." conflict therefore proves no trigger for this
// ID was accepted. Other responses before that lookup (notably 401/403/404) cannot
// disprove an earlier uncertain delivery. Do not classify every 4xx as rejection.
export function releaseRunNowAttempt(hadUncertainAttempt: boolean, dispatched: boolean, status?: number, message?: string) {
  if (!hadUncertainAttempt && !dispatched) return true;
  if (status === 409 && message === 'Automation changed.') return true;
  // These are pre-admission failures for a fresh, never-previously-dispatched ID.
  return !hadUncertainAttempt && status !== undefined && [400, 401, 403, 404, 409].includes(status);
}
