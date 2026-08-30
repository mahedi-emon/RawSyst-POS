// Getting back into an account whose password is lost.
//
// Both calls are unauthenticated, which is the point: the caller has lost the
// way to obtain a token.
//
// # `request` never tells you whether the account exists
//
// It resolves for a real address and an invented one. That is deliberate on the
// server — an endpoint that distinguishes them confirms which of a leaked
// address list are customers of this product — and it means the SCREEN must not
// promise more than it knows. "If that address is on an account, a code is on
// its way" is true; "check your email" implies a mail was sent, which the
// client cannot know and often will not be.
import type { Client } from './client';

/** Asks for a code. Resolves whether or not the address is on an account. */
export function requestPasswordReset(
  client: Client,
  email: string,
): Promise<void> {
  return client.send<void>('POST', '/api/v1/auth/forgot-password', { email });
}

/** Exchanges a code for a new password.
 *
 *  Rejects with one message for a wrong, expired, spent or unknown code — the
 *  server does not distinguish them and neither should the screen. */
export function completePasswordReset(
  client: Client,
  email: string,
  code: string,
  newPassword: string,
): Promise<void> {
  return client.send<void>('POST', '/api/v1/auth/reset-password', {
    email,
    code,
    new_password: newPassword,
  });
}
