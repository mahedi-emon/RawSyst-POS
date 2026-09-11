// Which origin may serve which half of the application.
//
// The rule is small and the consequences of getting it wrong are not: too
// strict and the business application 404s its own pages, too loose and the
// control plane appears on a shop's hostname. Both directions are asserted.
//
// What these do NOT test, because it is not what this rule is for: whether a
// business user can reach the platform API. That is `RequireSuperAdmin` in the
// Go service, it answers 404, and `backend/internal/api/access_test.go` walks
// every route against it. This file tests the user interface boundary only.

import { describe, expect, it } from 'vitest';

import { isPlatformPath, normaliseHost, servesPath } from './origin';

const CONSOLE = 'console.example.com';
const APP = 'app.example.com';

describe('the origin rule when a console host is configured', () => {
  it('serves the control plane on the console and nowhere else', () => {
    expect(servesPath(CONSOLE, CONSOLE, '/platform')).toBe(true);
    expect(servesPath(CONSOLE, CONSOLE, '/platform/backups')).toBe(true);

    // The thing this exists to prevent: the control plane answering on the
    // hostname every shop uses.
    expect(servesPath(CONSOLE, APP, '/platform')).toBe(false);
    expect(servesPath(CONSOLE, APP, '/platform/backups')).toBe(false);
  });

  it('serves the business application everywhere except the console', () => {
    expect(servesPath(CONSOLE, APP, '/')).toBe(true);
    expect(servesPath(CONSOLE, APP, '/dashboard')).toBe(true);
    expect(servesPath(CONSOLE, APP, '/pos')).toBe(true);

    // An operator who opens a shop screen on the console gets nothing. The
    // control plane is not a place to run a business from, and a console that
    // quietly served the business app would be one tenant-switching bug away
    // from being one.
    expect(servesPath(CONSOLE, CONSOLE, '/')).toBe(false);
    expect(servesPath(CONSOLE, CONSOLE, '/dashboard')).toBe(false);
    expect(servesPath(CONSOLE, CONSOLE, '/pos')).toBe(false);
  });

  it('keeps the API proxy on both origins', () => {
    // Load-bearing. The console signs in through the same endpoint, and it has
    // to reach it on its OWN origin or the SameSite=Strict refresh cookie is
    // never sent. Breaking this is how a separate origin ends up needing CORS
    // and a weakened cookie, which is the trade this whole design avoids.
    for (const host of [APP, CONSOLE]) {
      expect(servesPath(CONSOLE, host, '/api/v1/auth/login')).toBe(true);
      expect(servesPath(CONSOLE, host, '/api/v1/auth/refresh')).toBe(true);
      expect(servesPath(CONSOLE, host, '/api/v1/auth/logout')).toBe(true);
      expect(servesPath(CONSOLE, host, '/api/v1/platform/tenants')).toBe(true);
    }
  });

  it('keeps sign-in and recovery on both origins', () => {
    for (const host of [APP, CONSOLE]) {
      expect(servesPath(CONSOLE, host, '/login')).toBe(true);
      expect(servesPath(CONSOLE, host, '/forgot-password')).toBe(true);
      expect(servesPath(CONSOLE, host, '/reset-password')).toBe(true);
      expect(servesPath(CONSOLE, host, '/change-password')).toBe(true);
    }
  });

  it('keeps the framework and browser files on both origins', () => {
    for (const host of [APP, CONSOLE]) {
      expect(servesPath(CONSOLE, host, '/_next/chunk.js')).toBe(true);
      expect(servesPath(CONSOLE, host, '/favicon.ico')).toBe(true);
    }
  });

  it('ignores case and port, because the browser does', () => {
    expect(servesPath(CONSOLE, 'CONSOLE.example.com', '/platform')).toBe(true);
    expect(servesPath(CONSOLE, 'console.example.com:3000', '/platform')).toBe(true);
    expect(servesPath('CONSOLE.EXAMPLE.COM', CONSOLE, '/platform')).toBe(true);
  });

  it('is not fooled by a hostname that merely contains the console name', () => {
    // `console.example.com.attacker.test` must not count as the console, and
    // neither must a prefix of it. A substring check would accept both.
    expect(servesPath(CONSOLE, 'console.example.com.attacker.test', '/platform'))
      .toBe(false);
    expect(servesPath(CONSOLE, 'notconsole.example.com', '/platform')).toBe(false);
    expect(servesPath(CONSOLE, 'example.com', '/platform')).toBe(false);
  });

  it('does not treat a path that merely starts with the word as the plane', () => {
    // `/platforms` is a business path and must keep working on the business
    // origin. A naive `startsWith('/platform')` would 404 it.
    expect(isPlatformPath('/platforms')).toBe(false);
    expect(servesPath(CONSOLE, APP, '/platforms')).toBe(true);
    expect(isPlatformPath('/platform')).toBe(true);
    expect(isPlatformPath('/platform/')).toBe(true);
    expect(isPlatformPath('/platform/tenants')).toBe(true);
  });
});

describe('the origin rule when no console host is configured', () => {
  it('changes nothing at all', () => {
    // Every deployment that has not opted in, and every developer machine.
    // Turning the split on is a deployment decision; it must not arrive as a
    // behaviour change underneath somebody who upgraded.
    for (const path of ['/', '/dashboard', '/platform', '/platform/backups']) {
      expect(servesPath('', APP, path)).toBe(true);
      expect(servesPath('   ', 'localhost', path)).toBe(true);
    }
  });
});

describe('normaliseHost', () => {
  it('strips the port and lower-cases', () => {
    expect(normaliseHost('Example.COM:8443')).toBe('example.com');
    expect(normaliseHost('example.com')).toBe('example.com');
  });

  it('does not mistake an IPv6 literal colon for a port', () => {
    expect(normaliseHost('[::1]:3000')).toBe('[::1]');
    expect(normaliseHost('[2001:db8::1]')).toBe('[2001:db8::1]');
  });
});
