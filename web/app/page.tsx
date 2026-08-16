// The back office is one screen behind a sign-in, so the route tree is one
// entry deep. Sections are state rather than URLs: they carry no deep links
// anybody shares, and a router here would be a second navigation model to keep
// in step with the POS's.

import { Providers } from '@/components/Providers';

export default function Page() {
  return <Providers />;
}
