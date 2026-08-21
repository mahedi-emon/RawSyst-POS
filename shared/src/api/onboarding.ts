// The setup wizard, blueprint A5.
//
// Four routes, and the client knows as little as possible about them. The
// server owns the step order — `next_step` comes back on every response for
// exactly that reason, so moving a step or inserting one stays a server change
// alone — and it owns what makes a step complete.
//
// # Answers are free-form, deliberately
//
// Each step carries different answers, so `step_data` is an open JSON object
// keyed by step name rather than a fixed shape. That is the server's design
// (`handleOnboardingSaveStep` reads raw JSON), and typing it rigidly here would
// invent a contract the server does not have.
//
// # Nothing here carries a secret
//
// Setup captures business identity and tax registration. It does not touch
// ZATCA credentials, OTPs, CSRs or private keys — those live in the EGS unit
// and its keystore, behind a separate permission, and none of them has a
// verified request format yet. A wizard that offered a field for an OTP would
// be inviting a client to paste something this product cannot yet use.

import type { Client } from './client';

/** The seven steps of A5, as the server names them. */
export type OnboardingStep =
  | 'business_info'
  | 'stores'
  | 'tax'
  | 'employees'
  | 'hardware'
  | 'opening_balances'
  | 'finished';

export interface OnboardingProgress {
  current_step: OnboardingStep;
  completed_steps: OnboardingStep[];
  /** Answers so far, keyed by step. Free-form per step. */
  step_data: Record<string, unknown> | null;
  finished: boolean;
  /** The server's own answer to "what comes next", so the client never has to
   *  know the order. */
  next_step?: OnboardingStep;
}

export function fetchOnboarding(client: Client): Promise<OnboardingProgress> {
  return client.send<OnboardingProgress>('GET', '/api/v1/onboarding');
}

/** Records a step's answers without committing them. A half-finished step
 *  survives the Owner closing the browser, which is what makes the wizard
 *  resumable rather than a single sitting. */
export function saveOnboardingStep(
  client: Client,
  step: OnboardingStep,
  answers: unknown,
): Promise<void> {
  return client.send<void>('PUT', `/api/v1/onboarding/steps/${step}`, answers);
}

/** Validates the step and advances. The server is the authority on whether a
 *  step is complete; field errors come back keyed as the form keys them. */
export function completeOnboardingStep(
  client: Client,
  step: OnboardingStep,
): Promise<OnboardingProgress> {
  return client.send<OnboardingProgress>(
    'POST',
    `/api/v1/onboarding/steps/${step}/complete`,
    {},
  );
}

/** Turns the answers into a real company. Idempotency is the server's: the
 *  plan ceiling is checked there, not trusted from here. */
export function commitOnboardingCompany(
  client: Client,
): Promise<{ company_id: string }> {
  return client.send<{ company_id: string }>('POST', '/api/v1/onboarding/company', {});
}
