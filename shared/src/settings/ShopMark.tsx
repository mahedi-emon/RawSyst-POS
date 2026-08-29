// The shop's own name and logo, at the top of its own software.
//
// A business that has uploaded a logo should see it. Until now the rail said
// "RawSyst" to every tenant — which is the vendor's name, not the shop's, and
// a shopkeeper looking at their own till software has no reason to be reminded
// whose product it is on every screen.
//
// # Why the image is fetched rather than pointed at
//
// The logo route is authenticated, and a browser does not attach an
// Authorization header to an `<img src>`. So the bytes come through the API
// client and become an object URL. That URL is a live reference into the
// document's memory: it has to be revoked when this unmounts or when the logo
// changes, or a session that visits Branding a few times leaks a blob each
// time.
//
// # Why a failure shows the name and no picture
//
// A logo that will not load is a cosmetic problem. A shop whose navigation
// disappeared because of one is not. Every failure here falls back to the
// wordmark, which is why there is no error state.
import { useEffect, useState } from 'react';

import { useAuth } from '../auth/session';
import { logoObjectURL } from '../api/branding';

export function ShopMark({
  companyId,
  name,
  /** Bumped by the branding screen after an upload, so the mark refetches
   *  rather than showing the old picture until the next reload. */
  version = 0,
}: {
  companyId: string | null;
  name: string | null;
  version?: number;
}) {
  const { client } = useAuth();
  const [logo, setLogo] = useState<string | null>(null);

  useEffect(() => {
    if (!companyId) {
      setLogo(null);
      return;
    }

    let url: string | null = null;
    let cancelled = false;

    void logoObjectURL(client, companyId)
      .then((made) => {
        // The component may have unmounted, or the company changed, while the
        // bytes were in flight. Revoking immediately rather than setting state
        // is what stops the leak in that case.
        if (cancelled) {
          URL.revokeObjectURL(made);
          return;
        }
        url = made;
        setLogo(made);
      })
      .catch(() => {
        // No logo set, or it could not be read. Both mean the same thing here:
        // show the name. A business with no logo is the ordinary state for a
        // new one, not a fault.
        if (!cancelled) setLogo(null);
      });

    return () => {
      cancelled = true;
      if (url) URL.revokeObjectURL(url);
    };
  }, [client, companyId, version]);

  // The initial, for a business with no logo yet. Its own first letter rather
  // than the vendor's R: a shop with no logo is still that shop.
  const initial = (name ?? 'R').trim().charAt(0).toUpperCase() || 'R';

  return (
    <>
      {logo ? (
        <img className="bo__logo" src={logo} alt="" />
      ) : (
        <span className="bo__mark" aria-hidden="true">
          {initial}
        </span>
      )}
      {/* The shop's name, falling back to the product's only when there is no
          business yet — during onboarding, before the first company exists. */}
      <span className="bo__wordmark">{name ?? 'RawSyst'}</span>
    </>
  );
}
