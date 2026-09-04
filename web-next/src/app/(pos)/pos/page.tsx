'use client';

// Either the counter chooser or the till, depending on whether this browser is
// bound to a counter yet. One route, because to a cashier it is one thing: they
// press Point of sale and they are selling.

import { CounterPicker } from '@/components/pos/counter-picker';
import { Till } from '@/components/pos/till';
import { useT } from '@/lib/i18n/locale';
import { useCounter } from '@/lib/pos/counter';

export default function PosPage() {
  const t = useT();
  const { state } = useCounter();

  if (state.kind === 'open') return <Till />;

  if (state.kind === 'opening') {
    return (
      <div className="grid min-h-dvh place-items-center bg-ground" aria-busy="true">
        <p className="text-body text-muted">{t('nx.pos.openingCounter')}</p>
      </div>
    );
  }

  return <CounterPicker />;
}
