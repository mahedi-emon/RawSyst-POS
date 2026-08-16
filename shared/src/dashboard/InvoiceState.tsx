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

const STATES: Record<string, { label: string; tone: string; hint: string }> = {
  draft: {
    label: 'Draft',
    tone: 'ds-badge--neutral',
    hint: 'Not yet a legal document. No chain position used.',
  },
  signed_pending_report: {
    label: 'Awaiting reporting',
    tone: 'ds-badge--warning',
    hint: 'The sale is recorded and the receipt is valid. Reporting to ZATCA is outstanding.',
  },
  signed_pending_clear: {
    label: 'Awaiting clearance',
    tone: 'ds-badge--warning',
    hint: 'A B2B invoice waiting to be cleared before it is issued.',
  },
  uncleared_issued: {
    label: 'Issued uncleared',
    tone: 'ds-badge--warning',
    hint: 'Issued during an extended outage, to be cleared when the service returns.',
  },
  submitted: {
    label: 'Submitted',
    tone: 'ds-badge--info',
    hint: 'Sent, awaiting an answer.',
  },
  cleared: {
    label: 'Cleared',
    tone: 'ds-badge--success',
    hint: 'Accepted by ZATCA.',
  },
  reported: {
    label: 'Reported',
    tone: 'ds-badge--success',
    hint: 'Accepted by ZATCA.',
  },
  rejected: {
    label: 'Rejected',
    tone: 'ds-badge--danger',
    hint: 'ZATCA refused it. It needs looking at.',
  },
  cancelled: {
    label: 'Cancelled',
    tone: 'ds-badge--neutral',
    hint: 'No longer in the chain.',
  },
};

export function InvoiceState({ state }: { state: string }) {
  const known = STATES[state];

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

/** The plain-language explanation, for screens with room for a sentence. */
export function invoiceStateHint(state: string): string {
  return STATES[state]?.hint ?? '';
}
