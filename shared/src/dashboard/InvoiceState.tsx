// An invoice's position in the ZATCA lifecycle, in words a shop owner reads.
//
// The state names in the database are precise and are not English: nobody
// outside this codebase knows what `signed_pending_report` means, and an owner
// shown it will either ignore the column or ask. Both are failures.
//
// # It must never overstate
//
// "Reported" means ZATCA accepted it. Nothing here may say that of an invoice
// that has not been submitted, and while the P1 verification gate is open none
// have been. So the pending states are worded as pending — not as "processing",
// which implies something is happening, and not with a spinner, which implies
// it will finish shortly.

import { useT } from '../i18n/locale';
import type { Key } from '../i18n/strings';

type StateView = { label: string; tone: string; hint: string };

// A function of the translator rather than a module constant. A constant is
// built once at import, before any locale exists, so it would freeze whichever
// language the bundle happened to load with and never change on a switch.
function stateViews(t: (key: Key) => string): Record<string, StateView> {
  const acceptedByZatca = t('invState.acceptedByZatca');
  return {
    draft: {
      label: t('common.draft'),
      tone: 'ds-badge--neutral',
      hint: t('invState.draftHint'),
    },
    signed_pending_report: {
      label: t('invState.awaitingReporting'),
      tone: 'ds-badge--warning',
      hint: t('invState.awaitingReportingHint'),
    },
    signed_pending_clear: {
      label: t('invState.awaitingClearance'),
      tone: 'ds-badge--warning',
      hint: t('invState.awaitingClearanceHint'),
    },
    uncleared_issued: {
      label: t('invState.issuedUncleared'),
      tone: 'ds-badge--warning',
      hint: t('invState.issuedUnclearedHint'),
    },
    submitted: {
      label: t('invState.submitted'),
      tone: 'ds-badge--info',
      hint: t('invState.submittedHint'),
    },
    cleared: { label: t('invState.cleared'), tone: 'ds-badge--success', hint: acceptedByZatca },
    reported: { label: t('invState.reported'), tone: 'ds-badge--success', hint: acceptedByZatca },
    rejected: {
      label: t('invState.rejected'),
      tone: 'ds-badge--danger',
      hint: t('invState.rejectedHint'),
    },
    cancelled: {
      label: t('invState.cancelled'),
      tone: 'ds-badge--neutral',
      hint: t('invState.cancelledHint'),
    },
  };
}

export function InvoiceState({ state }: { state: string }) {
  const t = useT();
  const known = stateViews(t)[state];

  // An unknown state is shown as itself rather than guessed at. A state added
  // server-side must not be silently rendered as something reassuring.
  const label = known?.label ?? state.replace(/_/g, ' ');
  const tone = known?.tone ?? 'ds-badge--neutral';

  return (
    <span className={`ds-badge ${tone}`} title={known?.hint}>
      {label}
    </span>
  );
}

/** The plain-language explanation, for screens with room for a sentence.
 *
 * Takes the translator because the hints are catalogue entries now; a module
 * constant could not have been translated at all. */
export function invoiceStateHint(state: string, t: (key: Key) => string): string {
  return stateViews(t)[state]?.hint ?? '';
}
