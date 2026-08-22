// The string catalogue.
//
// Blueprint G3 asks for "full RTL layout mirroring for Arabic (menus, receipts,
// invoices, dashboards) — not just translated text sitting in an LTR layout".
// The layout half is CSS, and the stylesheets already use logical properties
// throughout so they mirror on `dir="rtl"` without a second sheet. This file is
// the other half.
//
// # Why a flat catalogue rather than a library
//
// The product ships two locales, both known at build time, with no plural
// rules more complex than "1 payment / n payments" and no runtime locale
// negotiation. An i18n framework would add a dependency, a loader and an
// async boundary to solve problems this product does not have. A typed record
// gives the one property that matters: a key that exists in `en` and not in
// `ar` fails to compile.
//
// # What is deliberately NOT here
//
// Data. A product name, a supplier's legal name, a customer's address and the
// Arabic blocks a shop writes on its own invoices are the tenant's content, not
// ours — they live in `*_ar` columns and render as written. Translating those
// would mean machine-translating a company's own name onto its tax invoice.
//
// Money and dates. Design system §6 rule 4: numbers stay LTR inside Arabic
// text, and `SAR 1,150.00` reads left to right in both languages. Arabic-Indic
// numerals are a per-tenant preference that is off by default, so Western
// digits are correct in both locales and no formatting branches on locale.

/** The locales this product ships. */
export type Locale = 'en' | 'ar';

/** Text direction, derived rather than stored — there is one right answer. */
export function directionOf(locale: Locale): 'ltr' | 'rtl' {
  return locale === 'ar' ? 'rtl' : 'ltr';
}

/**
 * The English catalogue, which is also the key list.
 *
 * `Key` is derived from this object, so every other locale is checked against
 * it by the compiler.
 */
export const en = {
  // --- Chrome and navigation ---------------------------------------------
  'nav.dashboard': 'Dashboard',
  'nav.buying': 'Buying',
  'nav.customers': 'Customers',
  'nav.settlement': 'Settlement',
  'nav.devices': 'Terminals',
  'nav.einvoicing': 'E-invoicing',
  'nav.inventory': 'Inventory',
  'nav.returns': 'Returns',
  'nav.branding': 'Branding',
  'nav.setup': 'Setup',
  'nav.sections': 'Sections',
  'nav.signOut': 'Sign out',
  'nav.company': 'Company',

  // --- Actions -----------------------------------------------------------
  'action.save': 'Save',
  'action.cancel': 'Cancel',
  'action.edit': 'Edit',
  'action.add': 'Add',
  'action.remove': 'Remove',
  'action.retry': 'Try again',
  'action.close': 'Close',
  'action.search': 'Search',
  'action.back': 'Back',
  'action.saving': 'Saving…',
  'action.loading': 'Loading…',

  // --- Common states -----------------------------------------------------
  'state.empty': 'Nothing to show yet',
  'state.offline': 'Offline',
  'state.online': 'Online',
  'state.failed': 'That did not work',
  'state.optional': '(optional)',

  // --- Sign in -----------------------------------------------------------
  'login.title': 'Sign in',
  'login.email': 'Email',
  'login.password': 'Password',
  'login.submit': 'Sign in',
  'login.working': 'Signing in…',
  'login.chooseTenant': 'Which business?',

  // --- The counter (UI spec §1) ------------------------------------------
  'pos.title': 'Counter',
  'pos.scan': 'Scan or search',
  'pos.scanHint': 'Scan a barcode, or type a name or SKU',
  'pos.cart': 'Cart',
  'pos.cartEmpty': 'Nothing in the cart yet',
  'pos.qty': 'Qty',
  'pos.price': 'Price',
  'pos.lineTotal': 'Total',
  'pos.subtotal': 'Subtotal',
  'pos.vat': 'VAT',
  'pos.total': 'Total',
  'pos.discount': 'Discount',
  'pos.pay': 'Pay',
  'pos.complete': 'Complete sale',
  'pos.hold': 'Hold',
  'pos.resume': 'Resume',
  'pos.void': 'Void',
  'pos.change': 'Change',
  'pos.customer': 'Customer',
  'pos.noCustomer': 'Walk-in',
  'pos.receipt': 'Receipt',
  'pos.print': 'Print',
  'pos.newSale': 'New sale',
  'pos.queued': 'Saved on this terminal',
  'pos.queuedHint': 'It will reach the server when the connection returns.',

  // --- Tenders -----------------------------------------------------------
  'tender.cash': 'Cash',
  'tender.mada': 'Mada',
  'tender.card': 'Card',
  'tender.transfer': 'Bank transfer',
  'tender.storeCredit': 'Store credit',
  'tender.onAccount': 'On account',

  // --- Shift (UI spec §7) -------------------------------------------------
  'shift.title': 'Shift',
  'shift.open': 'Open shift',
  'shift.close': 'Close shift',
  'shift.openingFloat': 'Opening float',
  'shift.counted': 'Counted',
  'shift.expected': 'Expected',
  'shift.difference': 'Difference',
  'shift.over': 'Over',
  'shift.short': 'Short',
  'shift.exact': 'Exact',
  'shift.xReport': 'X report',
  'shift.zReport': 'Z report',
  'shift.cashDrop': 'Cash drop',

  // --- Settlement ---------------------------------------------------------
  'settlement.title': 'Card settlement',
  'settlement.pending': 'Awaiting settlement',
  'settlement.record': 'Record a deposit',
  'settlement.reference': 'Bank statement reference',
  'settlement.depositedOn': 'Date it landed',
  'settlement.netAmount': 'Amount deposited',
  'settlement.fee': 'Fee',
  'settlement.gross': 'Taken',

  // --- The counter, continued ---------------------------------------------
  'pos.currentSale': 'Current sale',
  'pos.heldSales': 'Held sales',
  'pos.outstanding': 'Outstanding',
  'pos.paymentsTaken': 'Payments taken',
  'pos.item': 'Item',
  'pos.scanBarcode': 'Scan a barcode',
  'pos.scanPlaceholder': 'Scan or type a barcode',
  'pos.scanToBegin': 'Scan an item to begin.',
  'pos.tillNotOpen': 'This till is not open.',
  'pos.tillNotOpenWhy':
    'Count the float into the drawer under Till before ringing up sales — a sale has to belong to a shift somebody can reconcile.',
  'pos.shiftOpenSince': 'Shift {no} · open since {time}',
  'pos.totalsAndPayment': 'Totals and payment',
  'pos.till': 'Till',

  // --- Sign in, continued ---------------------------------------------------
  'login.continue': 'Sign in to continue',
  'login.openTill': 'Sign in to open the till',

  // --- Shift, continued -----------------------------------------------------
  'shift.openTill': 'Open the till',
  'shift.closeTill': 'Close the till',
  'shift.blindClose': 'Blind close',
  'shift.confirmClose': 'Confirm the close',
  'shift.countDrawer': 'Count the drawer',
  'shift.countedCash': 'Counted cash',
  'shift.expectedInDrawer': 'Expected in the drawer',
  'shift.howToCount': 'How to count',
  'shift.howMuch': 'How much',
  'shift.whatFor': 'What for',
  'shift.moveCash': 'Move cash',
  'shift.safeDrop': 'Midday drop to the safe',
  'shift.closed': 'Closed',
  'shift.sales': 'Sales',
  'shift.notes': 'Anything worth recording about this shift',
  'shift.notReckoned': 'The drawer could not be reckoned',

  // --- Returns and exchanges ------------------------------------------------
  'returns.return': 'Return',
  'returns.refund': 'Refund',
  'returns.exchange': 'Going out in exchange',
  'returns.credit': 'Credit for goods returned',
  'returns.saleReference': 'Sale reference',
  'returns.scanReceipt': 'Scan the receipt or type the sale reference',
  'returns.scanReplacement': 'Scan the replacement',
  'returns.scanReplacementItem': 'Scan the replacement item',
  'returns.reason': 'Why is it coming back?',

  // --- Language -----------------------------------------------------------
  'language.label': 'Language',
  'language.english': 'English',
  'language.arabic': 'العربية',
} as const;

/** Every string the interface can show. */
export type Key = keyof typeof en;

/**
 * The Arabic catalogue.
 *
 * `Record<Key, string>` rather than a partial: a key added to `en` without an
 * Arabic string is a compile error, not a screen that silently falls back to
 * English on a Saudi shop's till.
 *
 * Retail and accounting terms use the wording ZATCA's own Arabic materials use
 * where they have one — فاتورة ضريبية for tax invoice, ضريبة القيمة المضافة for
 * VAT — because a shop reading our screen and reading a ZATCA notice should see
 * the same words. Terms with no official Arabic source are ordinary usage and
 * are listed in the audit as wanting a native reviewer before release.
 */
export const ar: Record<Key, string> = {
  'nav.dashboard': 'لوحة المعلومات',
  'nav.buying': 'المشتريات',
  'nav.customers': 'العملاء',
  'nav.settlement': 'التسويات',
  'nav.devices': 'نقاط البيع',
  'nav.einvoicing': 'الفوترة الإلكترونية',
  'nav.inventory': 'المخزون',
  'nav.returns': 'المرتجعات',
  'nav.branding': 'الهوية',
  'nav.setup': 'الإعداد',
  'nav.sections': 'الأقسام',
  'nav.signOut': 'تسجيل الخروج',
  'nav.company': 'الشركة',

  'action.save': 'حفظ',
  'action.cancel': 'إلغاء',
  'action.edit': 'تعديل',
  'action.add': 'إضافة',
  'action.remove': 'حذف',
  'action.retry': 'حاول مرة أخرى',
  'action.close': 'إغلاق',
  'action.search': 'بحث',
  'action.back': 'رجوع',
  'action.saving': 'جارٍ الحفظ…',
  'action.loading': 'جارٍ التحميل…',

  'state.empty': 'لا يوجد شيء لعرضه بعد',
  'state.offline': 'غير متصل',
  'state.online': 'متصل',
  'state.failed': 'لم تنجح العملية',
  'state.optional': '(اختياري)',

  'login.title': 'تسجيل الدخول',
  'login.email': 'البريد الإلكتروني',
  'login.password': 'كلمة المرور',
  'login.submit': 'تسجيل الدخول',
  'login.working': 'جارٍ تسجيل الدخول…',
  'login.chooseTenant': 'أي منشأة؟',

  'pos.title': 'نقطة البيع',
  'pos.scan': 'امسح أو ابحث',
  'pos.scanHint': 'امسح الباركود، أو اكتب الاسم أو رمز الصنف',
  'pos.cart': 'السلة',
  'pos.cartEmpty': 'السلة فارغة',
  'pos.qty': 'الكمية',
  'pos.price': 'السعر',
  'pos.lineTotal': 'الإجمالي',
  'pos.subtotal': 'المجموع الفرعي',
  'pos.vat': 'ضريبة القيمة المضافة',
  'pos.total': 'الإجمالي',
  'pos.discount': 'الخصم',
  'pos.pay': 'الدفع',
  'pos.complete': 'إتمام البيع',
  'pos.hold': 'تعليق',
  'pos.resume': 'استئناف',
  'pos.void': 'إلغاء',
  'pos.change': 'المتبقي',
  'pos.customer': 'العميل',
  'pos.noCustomer': 'عميل عابر',
  'pos.receipt': 'الإيصال',
  'pos.print': 'طباعة',
  'pos.newSale': 'بيع جديد',
  'pos.queued': 'محفوظ على هذا الجهاز',
  'pos.queuedHint': 'سيصل إلى الخادم عند عودة الاتصال.',

  'tender.cash': 'نقدًا',
  'tender.mada': 'مدى',
  'tender.card': 'بطاقة',
  'tender.transfer': 'تحويل بنكي',
  'tender.storeCredit': 'رصيد لدى المتجر',
  'tender.onAccount': 'على الحساب',

  'shift.title': 'الوردية',
  'shift.open': 'فتح الوردية',
  'shift.close': 'إغلاق الوردية',
  'shift.openingFloat': 'الرصيد الافتتاحي',
  'shift.counted': 'المعدود',
  'shift.expected': 'المتوقع',
  'shift.difference': 'الفرق',
  'shift.over': 'زيادة',
  'shift.short': 'عجز',
  'shift.exact': 'مطابق',
  'shift.xReport': 'تقرير X',
  'shift.zReport': 'تقرير Z',
  'shift.cashDrop': 'إيداع نقدي',

  'settlement.title': 'تسوية البطاقات',
  'settlement.pending': 'في انتظار التسوية',
  'settlement.record': 'تسجيل إيداع',
  'settlement.reference': 'مرجع كشف الحساب',
  'settlement.depositedOn': 'تاريخ الإيداع',
  'settlement.netAmount': 'المبلغ المودع',
  'settlement.fee': 'الرسوم',
  'settlement.gross': 'المحصّل',

  'pos.currentSale': 'البيع الحالي',
  'pos.heldSales': 'المبيعات المعلّقة',
  'pos.outstanding': 'المتبقي للسداد',
  'pos.paymentsTaken': 'المدفوعات المستلمة',
  'pos.item': 'الصنف',
  'pos.scanBarcode': 'امسح الباركود',
  'pos.scanPlaceholder': 'امسح أو اكتب الباركود',
  'pos.scanToBegin': 'امسح صنفًا للبدء.',
  'pos.tillNotOpen': 'نقطة البيع غير مفتوحة.',
  'pos.tillNotOpenWhy':
    'عُدّ الرصيد الافتتاحي في الدرج من قسم الصندوق قبل تسجيل أي بيع — كل عملية بيع يجب أن تنتمي إلى وردية يمكن مطابقتها.',
  'pos.shiftOpenSince': 'وردية {no} · مفتوحة منذ {time}',
  'pos.totalsAndPayment': 'الإجماليات والدفع',
  'pos.till': 'الصندوق',

  'login.continue': 'سجّل الدخول للمتابعة',
  'login.openTill': 'سجّل الدخول لفتح الصندوق',

  'shift.openTill': 'فتح الصندوق',
  'shift.closeTill': 'إغلاق الصندوق',
  'shift.blindClose': 'إغلاق أعمى',
  'shift.confirmClose': 'تأكيد الإغلاق',
  'shift.countDrawer': 'عدّ الدرج',
  'shift.countedCash': 'النقد المعدود',
  'shift.expectedInDrawer': 'المتوقع في الدرج',
  'shift.howToCount': 'طريقة العدّ',
  'shift.howMuch': 'المبلغ',
  'shift.whatFor': 'السبب',
  'shift.moveCash': 'تحويل نقدي',
  'shift.safeDrop': 'إيداع منتصف اليوم في الخزنة',
  'shift.closed': 'مغلقة',
  'shift.sales': 'المبيعات',
  'shift.notes': 'أي ملاحظة تستحق التسجيل عن هذه الوردية',
  'shift.notReckoned': 'تعذّر حساب الدرج',

  'returns.return': 'إرجاع',
  'returns.refund': 'استرداد',
  'returns.exchange': 'الصادر في الاستبدال',
  'returns.credit': 'إشعار دائن للبضاعة المرتجعة',
  'returns.saleReference': 'مرجع البيع',
  'returns.scanReceipt': 'امسح الإيصال أو اكتب مرجع البيع',
  'returns.scanReplacement': 'امسح البديل',
  'returns.scanReplacementItem': 'امسح الصنف البديل',
  'returns.reason': 'ما سبب الإرجاع؟',

  'language.label': 'اللغة',
  'language.english': 'English',
  'language.arabic': 'العربية',
};

/** The catalogues, by locale. */
export const catalogues: Record<Locale, Record<Key, string>> = { en, ar };
