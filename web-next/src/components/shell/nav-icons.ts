// The icons navigation is allowed to name.
//
// # Why this file exists instead of a dynamic lookup
//
// `navigation.ts` holds an icon NAME so it can stay free of React and be tested
// as data. The obvious way to resolve that name is
// `import * as icons from 'lucide-react'` and index it -- which is a barrel
// import, and it defeats tree-shaking completely: the bundler cannot know which
// of the fifteen hundred icons the string will select, so it keeps all of them.
//
// An explicit map costs one line per icon and lets the bundler drop the rest.
// It also fails loudly: an icon named in the navigation that is not here shows
// the fallback rather than silently rendering nothing.

import {
  Activity,
  Boxes,
  Building2,
  ChartNoAxesColumn,
  Circle,
  IdCard,
  LayoutDashboard,
  Package,
  Scale,
  Settings,
  ShieldCheck,
  ShoppingCart,
  Truck,
  Users,
  Wallet,
  Wrench,
  type LucideIcon,
} from 'lucide-react';

const ICONS: Readonly<Record<string, LucideIcon>> = {
  Activity,
  Boxes,
  Building2,
  ChartNoAxesColumn,
  IdCard,
  LayoutDashboard,
  Package,
  Scale,
  Settings,
  ShieldCheck,
  ShoppingCart,
  Truck,
  Users,
  Wallet,
  Wrench,
};

/** Resolves the icon a navigation section names. */
export function iconFor(name: string): LucideIcon {
  return ICONS[name] ?? Circle;
}

export { Lock } from 'lucide-react';
