# ZATCA E-Invoicing Phase 2 & POS Requirements: Verification Against Official Sources

## TL;DR
- **Most of the 17 points are confirmed by official ZATCA primary sources**, but three areas require caution: the widely-quoted "escalating fine ladder" (warning → SAR 1,000 → 5,000 → 10,000 → 40,000) is NOT stated in ZATCA's primary e-invoicing fines announcement and appears to be a secondary-source reconstruction of the separate VAT field-control reclassification decision; the CSID validity period is not published as a clean fixed number (official docs say "up to 60 months / 5 years" embedded per-certificate); and several precise figures (test-invoice counts, retention "15 years") are derived rather than stated verbatim.
- **Confirmed hard facts:** XML (not PDF/A-3) is mandatory for the actual submission to Fatoora for both clearance and reporting; CSID is issued per EGS Unit (not necessarily per physical terminal); Arabic is mandatory on invoices; half-up rounding to 2 decimals; distinct API response categories (200/202 accepted-with-warnings vs 400 rejected); buyer VAT number required on B2B standard invoices; invoice type/title required; being "listed" by ZATCA is explicitly NOT a certification.
- **For the Regulatory Rule Registry:** treat the six specific CSR fields, prohibited functions (training mode is not named but the effect is prohibited), the extended-outage B2B procedure, and the 6/11/15-year retention tiers as confirmed; flag the fine ladder and CSID validity as "not fixed in a single primary e-invoicing source."

## Key Findings

Below, each of the 17 points is verified with the specific official source, the exact rule/figure, and a confidence rating.

---

### 1. FINE / PENALTY TABLE
**Claim checked:** Specific SAR fines for e-invoicing violations and a documented escalating ladder.

**What the official source says.** ZATCA's official news announcement "ZATCA releases the Violations and Fines related to E-invoicing" (zatca.gov.sa/en/MediaCenter/News/Pages/News_465.aspx, announcing Phase 1 from 4 Dec 2021) states verbatim:
- **Not issuing e-invoices** — "begins with a fine of SR 5,000."
- **Deleting or amending e-invoices after issuance** — "begins with a fine of SR 10,000."
- **Not including the QR code on a simplified tax invoice; not including the buyer's VAT number on the tax invoice; failure to notify the Authority of a malfunction** — "start with warning the facility."
- "All fines are applied according to the type of violation and the number of repetitions."

Deloitte's reproduction of the same official announcement shows the ranges as: Non-issuance/non-archiving = SAR 5,000–50,000; Deletion/amendment after issuance = SAR 10,000–50,000; QR/buyer-VAT/malfunction = warning first, up to SAR 50,000 maximum. The SAR 50,000 statutory ceiling ties to Article 45 of the VAT Law.

- **(a) Not issuing an e-invoice:** starts at SAR 5,000 (confirmed, official).
- **(b) Missing/incorrect QR code:** warning first (confirmed, official).
- **(c) Deleting/modifying an issued e-invoice:** starts at SAR 10,000 (confirmed, official).
- **(d) Failure to integrate by the deadline:** the primary e-invoicing fines announcement (a Phase-1 document) does NOT specify a discrete "non-integration" fine. The most specific secondary claim is from Jaicome ("ZATCA E-Invoicing Fines 2026"): "The fine for failing to integrate with the Fatoora platform is the harshest: it starts at SAR 10,000 on the second violation and climbs to SAR 50,000 after the sixth." This is NOT in a ZATCA primary document. **Could not be verified against an official primary source for the specific non-integration figure.**
- **(e) The escalating ladder (warning → SAR 1,000 → 5,000 → 10,000 → 40,000):** This specific ladder is repeated across many consulting/vendor pages and originates from ZATCA's **30 January 2022 reclassification decision for VAT field-control violations** (a general VAT-penalty reclassification), NOT from the e-invoicing fines announcement. Complyance ("Penalties for Saudi Arabia's ZATCA E-Invoicing and VAT Violations") ties the ladder explicitly to "the recent reclassification decision": after a three-month correction period, "starting with a SAR 1,000 penalty for a second-time violation... This pattern will continue for subsequent violations, with increased penalties of SAR 5,000, SAR 10,000, and SAR 40,000, respectively," within a 12-month repeat window. The e-invoicing fines announcement itself does not enumerate this ladder. **Confidence: the ladder is real for VAT field-control violations but is NOT stated verbatim in the primary e-invoicing fines page — flag as secondary-sourced.**

**Confidence:** (a)(b)(c) Confirmed by official source. (d)(e) Could not confirm the specific figures against a single primary e-invoicing source; the ladder derives from the separate VAT reclassification decision.

---

### 2. WAVE 24 THRESHOLD AND DEADLINE
**Claim checked:** Wave 24 criteria, deadline, notification model, and whether any wave came after.

**Official source:** ZATCA news "ZATCA Determines the Criteria for Selecting the Targeted Taxpayers in Wave 24 for 'Integration Phase' of E-invoicing" (zatca.gov.sa/en/Pages/news_1426.aspx).
- **(a) Threshold:** verbatim — "the Twenty-Forth Wave included all taxpayers whose revenues subject to VAT exceeded (SAR 375,000) during 2022, 2023 or 2024." So the criterion is **"exceeded SAR 375,000"** (a floor, not a 375k–750k band). It is a per-year test across any of those three years. (The formal instrument is ZATCA Governor Decision 287-99-1447, gazetted 26 September 2025, per secondary sources.)
- **(b) Deadline:** integrate with Fatoora "by no later than 30 June 2026."
- **(c) Notification model:** ZATCA "will notify all targeted taxpayers" and (per the standing rule) "would inform the following waves directly at least six months before their Integration Date." Obligation is **per-taxpayer/notification-based** — ZATCA notifies each targeted taxpayer via registered email & SMS; a business is not required to implement Phase 2 until it has been notified of its wave enforcement date. This confirms the reviewer's point: it is NOT an automatic blanket rule for all VAT-registered businesses above a threshold.
- **(d) Waves after Wave 24:** As of the research date (August 2026), Wave 24 (threshold SAR 375,000, deadline 30 June 2026) is the most recent announced wave confirmed against ZATCA primary source news_1426; no Wave 25 announcement was found.

**Confidence:** Confirmed by official source (threshold, deadline, notification model). No later wave found as of August 2026 (confirmed by absence).

---

### 3. XML vs PDF/A-3 FOR ZATCA SUBMISSION
**Claim checked:** Whether the actual API submission must be XML, with PDF/A-3 being only for the buyer's human-readable copy.

**Official source:** E-Invoicing Detailed Guideline (Version 2, May 2023), Sections 4.1.2 and 4.2.2, and the E-invoicing Detailed Technical Guideline (Version 2, Nov 2022) Section 4.3.
- Detailed Guideline §4.1.2(b): "Phase 2 (Integration Phase) Tax Invoices **must be submitted in XML format (not PDF/A-3)** to FATOORA Platform for 'Clearance' using APIs."
- Detailed Guideline §4.2.2(c): "Taxpayers must submit the Simplified Tax Invoices **in XML format (not PDF/A-3)** to FATOORA Platform for 'Reporting' within 24 hours."
- Technical Guideline §4.3: "Taxpayer's EGS unit(s) **must submit their documents to ZATCA in the XML format and not in the PDF A/3 format.**"
- Invoices may be *generated/stored* in XML or PDF/A-3 (with embedded XML), and the buyer copy may be XML or PDF/A-3. But the submission to Fatoora is XML only.

**Confidence:** Confirmed by official source. The distinction the reviewer raised is exactly correct.

---

### 4. CSID / EGS UNIT TERMINOLOGY
**Claim checked:** CSID is issued per "EGS Unit," not per physical terminal; and what defines an EGS Unit.

**Official source:** E-invoicing Detailed Technical Guideline (Nov 2022) §§2, 3, 3.5; Detailed Guideline §2.7, §2.10.
- CSID definition (Detailed Guideline §2.10): "a unique identifier that links the E-Invoice Solution Unit and a trusted third party... and uniquely identify their unit."
- An E-Invoice Solution "may contain one or more Units" (§2.7). CSID is issued per **EGS Unit**, not per physical terminal necessarily.
- The Technical Guideline §3.5 gives explicit architecture scenarios showing **one EGS Unit ≠ necessarily one physical POS device**:
  - Centralized server (on-premise or cloud): "one CSID per Taxpayer and also one CSID per unique sequence of generated documents."
  - Branch smart POS issuing/sending: "a CSID is required on each POS device."
  - Dumb/"standard" POS terminals with a central signing server: "if the POS devices are dumb terminals and the server stamps the invoices... then no CSIDs are required on the POS devices" — the CSID sits on the branch server or sending server instead.
- So an EGS Unit is the software unit that signs/generates a single invoice sequence; it maps to whatever component actually stamps invoices (a physical POS, a branch server, or a centralized cloud instance). One EGS Unit corresponds to one unique document sequence.

**Confidence:** Confirmed by official source. The reviewer's terminology point is correct: CSID is per EGS Unit, and an EGS Unit's mapping to hardware varies by architecture.

---

### 5. CSID VALIDITY PERIOD
**Claim checked:** Fixed validity (e.g., 1 yr compliance / 3 yr production) and renewal process.

**What official sources say.** ZATCA does NOT publish a clean, separately-fixed validity for Compliance CSID vs Production CSID in its primary PDFs. The only fixed numeric figure in official documentation is in the **Security Features Implementation Standards v1.2 (2023-05-19)**, X.509 certificate profile (§2.2.2), which defines the certificate's expiry (NotAfter) field as: **"Certificate generation process date/time + Up to 60 months (5 years)."** This is a per-certificate ceiling embedded in the X.509 NotAfter field, not a universally guaranteed term; the document notes the profile is illustrative pending ZATCA's published CP/CPS.
- On the **official ZATCA Fatoora Developer Community forum** (zatca1.discourse.group), a ZATCA staff moderator ("idaoud") stated on 21 January 2025: "the expiration of your PCSID after successfully onboarding is 5 years, after that you can renew or re-onboarding." A decoded sample certificate in another forum thread showed a ~5-year NotBefore/NotAfter span (2024-10-31 → 2029-10-30), consistent with 5 years.
- **Compliance CSID:** No fixed period is published; it is functionally a short-lived/temporary onboarding certificate generated by the e-invoicing platform (not ZATCA CA) used only to pass compliance checks. Claims of "1 year compliance / 3 years production" are third-party blog figures and are **NOT supported by any ZATCA primary source** (and one such 1-year claim directly contradicts the official 5-year ceiling).
- **Renewal process (confirmed, Technical Guideline §3.2.2):** "The process for the renewal of a CSID is similar to that of first-time onboarding; however, it involves the **revocation of the existing CSID and the issuance of a new one.**" ZATCA sends a reminder before expiry. Renewal is initiated via OTP + new CSR on the Fatoora Portal.

**Confidence:** Fixed "1yr/3yr" values could NOT be confirmed — they are contradicted by official sources. The authoritative figure is "up to 60 months (5 years)" (Security Features Standard v1.2 §2.2.2) and a 5-year PCSID confirmed by ZATCA forum staff. Renewal = revoke old + issue new (confirmed).

---

### 6. SANDBOX COMPLIANCE TESTING — NUMBER OF TEST INVOICES
**Claim checked:** Whether a fixed number of test invoices (6/10/12) is required to pass compliance and get a Compliance CSID.

**Official source:** E-invoicing Detailed Technical Guideline §3.3.4.2 (Completion of the Compliance Checks).
- The number of documents required is **conditional on the Functionality Map (Invoice Type) declared in the CSR**, not a single fixed count:
  - If Invoice Type = "1000" (Standard/B2B only): "the user should send 3 requests" — Standard Tax Invoice, Standard Debit Note, Standard Credit Note.
  - If Invoice Type = "0100" (Simplified/B2C only): "the user should send 3 requests" — Simplified Tax Invoice, Simplified Debit Note, Simplified Credit Note.
  - If "1100" (both), by extension the solution must pass the Standard set AND the Simplified set (i.e., 6 document types).
- So there is **no single universal fixed number** like "12." The count is 3 (single type) or 6 (both types), driven by the declared Functionality Map. ZATCA describes required compliance scenarios by document type rather than a fixed headline count.

**Confidence:** Confirmed by official source. The correct framing: **3 documents per invoice-type family (Standard or Simplified), i.e., up to 6** — not a fixed "12."

---

### 7. OFFLINE / EXTENDED CONNECTIVITY OUTAGE PROCEDURE (B2B STANDARD)
**Claim checked:** Whether the Detailed Guideline describes an exceptional procedure for extended outages preventing real-time B2B clearance, and any time threshold.

**Official source:** E-Invoicing Detailed Guideline §10 "Failure Scenarios" (the B2B failure diagrams, pp. 47–52).
- **Short/temporary ZATCA outage (B2B):** the seller retries clearance for ~5 minutes ("timing TBC"), and if ZATCA remains non-responsive, "shares uncleared invoice, keeps records of the transaction (Art. 7.5) and confirms the existing contact details of buyer," and continues attempting clearance in regular intervals (~every 15 minutes, "timing TBC"). Once clearance succeeds, the cleared e-invoice is shared with the buyer. "In case of extended outage, ZATCA might notify taxpayers on its website."
- **Extended internet connectivity outage (B2B):** The Guideline states that because "As per Article 53(1) of KSA VAT Regulations, Standard Tax Invoices (B2B) can be issued within 15 days from the end of the month in which supply takes place," the seller may process the transaction, share the **uncleared invoice** (which "will not be considered fully compliant but will be considered as a VAT invoice until fully compliant invoice is issued immediately once the connection is restored"), keep records under Art. 7.5, notify ZATCA via the dedicated failure-notification form, and once connectivity returns, submit for clearance and re-issue.
- **Key caveats stated by ZATCA:** "Uncleared invoices will not be eligible for VAT deduction. TP should keep evidence of trying to clear the invoice to ZATCA (e.g. API log)." And frequent/extended failure reporting will trigger individual ZATCA investigation.
- **Time threshold defining "extended outage":** There is **no single hard numeric threshold**; the operational effective window is tied to the 15-days-from-month-end invoicing deadline in VAT Art. 53(1), and short-outage retry intervals (~5 min, ~15 min) are marked "TBC." So "extended" is bounded functionally by the 15-day statutory issuance window, not by a fixed outage-duration number.

**Confidence:** Confirmed by official source (procedure exists; the operative window is the 15-day statutory issuance period; retry intervals are non-binding "TBC").

---

### 8. VAT / E-INVOICE RECORD RETENTION PERIODS
**Claim checked:** 6 years general; 11 years movable capital assets; 15 years immovable (real estate).

**Official source:** VAT Implementing Regulations, Article 66 (record-keeping). The rule as reproduced from the Regulations: invoices/books/records must be kept "for a minimum period of six (6) years from the end of the Tax Period to which they relate"; records for Capital Assets must be kept for "the Adjustment Period for these Capital Assets prescribed in article 50... plus five (5) years."
- Adjustment period (Art. 50/52): movable (tangible & intangible) capital assets = 6 years; immovable assets (real estate) = 10 years.
- Therefore: **(a) general = 6 years** (confirmed, primary). **(b) movable capital assets = 6 + 5 = 11 years** (confirmed by arithmetic on the primary rule). **(c) immovable/real estate = 10 + 5 = 15 years** (confirmed by arithmetic on the primary rule).
- The E-Invoicing Detailed Guideline cross-refers record-keeping to the VAT Law/Regulations (§7.3, §5.5) rather than restating the numbers.

**Confidence:** Confirmed. The "6 / 11 / 15 years" tiers are correct; the 11 and 15 are derived (adjustment period + 5 years) from Article 66 read with Article 50, which is exactly how ZATCA and all practitioners state them.

---

### 9. PROHIBITED EGS FUNCTIONALITIES
**Claim checked:** Prohibitions on (a) training/secret sales mode, (b) ICV reset/manipulation, (c) multiple parallel sequences, (d) private-key export.

**Official source:** E-Invoicing Detailed Guideline §5.6 and §6.5 (Prohibited Functions), §5.4 (Information Security); Security Features Implementation Standards.
- **(a) "Training mode"/secret bypass mode:** ZATCA does NOT use the phrase "training mode." However, the effect is prohibited via multiple rules: no "Anonymous access"; all activities must be logged with "user session management"; "Non-sequential log generation" is prohibited; and every invoice must be chained via Previous Invoice Hash. A hidden mode that records sales outside the official sequence would violate the single-chain and logging requirements. **Effect prohibited (confirmed); exact phrase "training mode" not used by ZATCA.**
- **(b) Invoice Counter (ICV) reset/manipulation:** Explicitly prohibited. §5.6/§6.5: "Invoice counter reset — The E-Invoice Solution must not provide a feature where the invoice counting can be reset." §5.4: "Resetting the invoice counter should not be a function... access to the counter value should be protected from system users." (Confirmed, official.)
- **(c) Multiple/parallel sequences:** Explicitly prohibited. §5.6/§6.5: "Allow ability to generate more than one invoice sequence at any given time — The E-Invoice Solution unit must not generate more than one sequence so that all invoices... are linked using 'Previous Invoice Hash' value into a single chain." (Confirmed, official.)
- **(d) Export of the CSID/private stamping key:** Explicitly prohibited. §6.5: "Export of stamping keys — The solution must not provide an option to export the cryptographic stamp private stamping key of the solution." §5.4: "Prevention of export of stamping keys: ...should be blocked by the solution vendor using a software or hardware key vault." (Confirmed, official.)
- Additional prohibited functions confirmed: default/factory passwords, log modification/deletion, inaccurate timestamps/time changes, alteration/deletion of issued invoices (cancellation only via credit note).

**Confidence:** (b)(c)(d) Confirmed verbatim by official source. (a) The concept is prohibited by effect (single-chain, logging, no anonymous access), but ZATCA does not use the term "training mode."

---

### 10. ARABIC LANGUAGE REQUIREMENT
**Claim checked:** Arabic mandatory as primary; English optional secondary.

**Official source:** E-Invoicing Detailed Guideline §5.7/§6.6 and the FAQ. §6.6: "The human readable format can be presented provided that it is in Arabic (in addition to any other language)." FAQ: "As per Article 53 of VAT Regulations, invoices must be in Arabic. The technical aspects of XML will be in English, however, the data for invoices (that will be visible once XML is visualized) must be in Arabic. Other languages can be present on the Invoice as well." Also: "Any information contained in the human readable form of the Tax Invoice must be in Arabic including notes. Any additional translation is also allowed."

**Confidence:** Confirmed by official source. Arabic is mandatory on the invoice (human-readable), with other languages permitted in addition. (XML technical tag names are English; invoice data content must be Arabic.)

---

### 11. ROUNDING RULES (2 DECIMALS / HALF-UP)
**Claim checked:** Whether an official rounding methodology is specified.

**Official source:** Electronic Invoice XML Implementation Standard (v1.2, 2023-05-19), §7.3 and the rounding section.
- "Rounding shall be performed by using 'half-up' rounding. Half-up means that half-way values are always rounded up... For rounding to two decimals, one uses the half-up rule on the third decimal."
- Monetary/VAT amounts are constrained to a maximum of 2 decimals (e.g., validation "The allowed maximum number of decimals for the Paid amount (BT-113) is 2"). The Detailed Guideline §8(e) also requires advance-payment amounts "rounded to two decimals."
- Note: Unit Price has no decimal-place restriction (Technical Guideline FAQ, referencing XML Implementation Standard §7.3).

**Confidence:** Confirmed by official source. Methodology = half-up rounding, applied on the third decimal to yield 2 decimals for monetary/VAT values.

---

### 12. ZATCA API RESPONSE TYPES (WARNINGS VS ERRORS)
**Claim checked:** Distinct statuses such as "accepted with warnings" vs "rejected with errors."

**Official source:** E-Invoicing Detailed Guideline §10 (Overview of responses) and Technical Guideline §4.3.
- Three validation outcomes: **Valid/compliant** (no fatal errors, no warnings); **Accepted with warnings** (no fatal errors but ≥1 warning — "temporarily accepted and might become rejections in the future"); **Rejected/Invalid** (≥1 fatal error → not a valid document).
- HTTP-style codes documented: **200** Action Successful; **202** Action Successful but with warnings; **303** Clearance switched off (submit via Reporting); **400** Action failed (rejected); **401** Unauthorized; **413** Payload too large; **429** Too many requests; **500/503/504** server errors.
- ZATCA's own labels are "Accepted," "Accepted with warnings," and "Rejected" (not the exact strings "PASSED_WITH_WARNINGS"/"REJECTED_WITH_ERRORS," which are vendor/community shorthand). For B2C reporting the API also returns statuses such as REPORTED / NOT_REPORTED; for clearance, CLEARED.
- Behavior: warnings still return a stamped/cleared document for B2B; a rejected document is not stamped and must be corrected and resubmitted as a new document (new ICV/hash).

**Confidence:** Confirmed by official source. The warnings-vs-errors distinction is real and documented; the exact string names "PASSED_WITH_WARNINGS"/"REJECTED_WITH_ERRORS" are not ZATCA's official labels — use 200/202/400 and "Accepted / Accepted with warnings / Rejected."

---

### 13. CSR METADATA FIELDS
**Claim checked:** Required CSR fields and their specifications.

**Official source:** E-invoicing Detailed Technical Guideline §3.3.3 (CSR inputs table) and §3.3.6 / Business FAQ. All CSR fields are mandatory; wrong format can cause CSR rejection. The fields:
- **Common Name** — Name or Asset Tracking Number for the Solution Unit (free text).
- **EGS Serial Number** — Manufacturer/Solution-Provider name, Model/Version and Serial Number; format example "1-<Manufacturer>|2-<Model/Version>|3-<Serial>."
- **Organization Identifier** — VAT or Group VAT Registration Number: "15 digits, starting and ending with 3."
- **Organization Unit Name** — the branch name; for VAT Groups this field "should contain the 10-digit TIN number of the individual group member whose EGS Unit is being onboarded" (rule: if 11th digit of the Organization Identifier = 1, it must be a 10-digit number).
- **Organization Name** — Taxpayer/Organization name.
- **Country Name** — 2-letter ISO 3166 Alpha-2 code.
- **Invoice Type (Functionality Map)** — digits mapped to "TSXY" using 0/1: e.g., 1000 = Standard only, 0100 = Simplified only, 1100 = both (X and Y reserved for future, set to 0).
- **Location** — branch/EGS address (preferably Saudi National Address short-address format).
- **Industry** — industry/sector.

**Confidence:** Confirmed by official source, with exact specifications.

---

### 14. BUYER VAT NUMBER ON B2B INVOICES
**Claim checked:** Whether ZATCA requires the buyer's VAT registration number on Standard (B2B) invoices.

**Official source:** ZATCA "How to Prepare?" Phase 1 page (zatca.gov.sa/en/E-Invoicing/PreparingYourBusiness/Phase1/Pages/How-to-prepare.aspx): "Tax Invoices (B2B): The VAT registration number of the buyer **if the buyer is a registered VAT taxpayer**, in addition to the invoice type." Also the fines announcement lists "not including the buyer VAT registration number on the tax invoice" as a violation (warning-first). The Detailed Guideline Tax Invoice fields derive from VAT Implementing Regulations Art. 53(5) and Annex 2.
- Nuance: it is required **when the buyer is VAT-registered.** For a B2B buyer who is not VAT-registered, a missing buyer VAT/additional ID may be "accepted with warnings" rather than a hard rejection (Detailed Guideline FAQ).

**Confidence:** Confirmed by official source. Buyer VAT number is a mandatory field on standard tax invoices where the buyer is VAT-registered.

---

### 15. INVOICE TYPE / TITLE REQUIREMENT
**Claim checked:** Whether invoices must display their type/title.

**Official source:** ZATCA "How to Prepare?" Phase 1 page explicitly lists, for both Tax Invoices and Simplified Tax Invoices, "the invoice type (description as a title)" as a required element. The invoice type code (BT-3 / KSA-2 etc.) is also a mandatory XML field. Secondary practitioner guides confirm the document must be titled "Tax Invoice" or "Simplified Tax Invoice" (and notes as Credit/Debit).

**Confidence:** Confirmed by official source. Invoice type/title is a required element.

---

### 16. SOLUTION PROVIDER LISTING / SELF-DECLARATION
**Claim checked:** Whether the "Listed Solution Provider" / self-declaration process equals ZATCA certification.

**Official source:** ZATCA FAQ "Are the listed/qualified providers certified by ZATCA?" (zatca.gov.sa/en/E-Invoicing/Introduction/FAQ/Pages/FAQ_037.aspx): "**No**, The list of the qualified invoicing solution providers has been built and published to facilitate the taxpayers['] access to the potential providers, and this list is **not considered as an approval by ZATCA of the solutions nor certification.**" The main E-Invoicing page adds: "Taxpayers may choose any e-invoicing solution provider as long as it is compliant... ZATCA will consider the taxpayer['s] compliance... even if the solution provider is not listed on the indicative list." ZATCA also disclaims liability for listed solutions.

**Confidence:** Confirmed by official source. The listing/self-declaration is an indicative, non-binding directory — explicitly NOT a certification or approval. Compliance responsibility remains with the taxpayer.

---

### 17. SDK VALIDATION vs OFFICIAL APPROVAL
**Claim checked:** Whether passing the SDK/validator constitutes ZATCA certification.

**Official source:** E-Invoicing Detailed Guideline §2.18 (the Toolkit is "testing toolkits provided by the Authority to allow Persons... to verify that their solutions generate compliant invoices"); Technical Guideline §2.1.4 (the Compliance & Enablement Toolbox / SDK and web validator are "optional" verification steps). The SDK is an offline self-check tool. There is no statement anywhere that passing the SDK/validator equals certification; combined with FAQ_037 (listing ≠ certification) and the Detailed Guideline disclaimer that the guide "is not considered mandatory... Every person subject to... Laws must check their duties... they are solely responsible for the compliance," ZATCA's position is that self-validation does not constitute official approval and compliance responsibility stays with the taxpayer/solution provider.

**Confidence:** Confirmed by official source (by combination of §2.18/§2.1.4 "optional verification" framing and the explicit non-certification statements). SDK validation is a developer self-check, NOT official certification.

---

## Details: Notable 2022–2026 documentation updates that affect these answers
- **Detailed Guideline** current version is **V2, May 2023**; **Detailed Technical Guideline** is **V2, Nov 2022**; **XML Implementation Standard** and **Security Features Implementation Standard** current versions are **v1.2, dated 2023-05-19**. Solutions should cite these versions in the Rule Registry.
- **VAT Implementing Regulations** were amended in 2025 (Official Gazette 18 April 2025), but the record-keeping periods (Art. 66) and adjustment periods (Art. 50/52) remained the retention basis for the 6/11/15-year tiers.
- **Wave 24** (threshold SAR 375,000; deadline 30 June 2026) is the newest wave. The **fine-cancellation/exemption initiative** is now confirmable against a ZATCA primary source: ZATCA's news "ZATCA Announces the Minister of Finance's Decision to Extend the Exemption of Fines Initiative" states it was extended "for an additional six months, starting from 1 July 2026" (i.e., running **through 31 December 2026**), and clarifies that any extension beyond 31 December 2026 "will not cover the fines associated with any return due for submission to the Authority after June 30, 2026."

## Recommendations
1. **Lock the confirmed rules into the Rule Registry now** with version citations: XML-only submission (Detailed Guideline §4.1.2/§4.2.2; Technical Guideline §4.3); half-up rounding to 2 decimals (XML Implementation Standard v1.2 §7.3); CSR field specs (Technical Guideline §3.3.3); prohibited functions (Detailed Guideline §5.6/§6.5); Arabic mandatory (§6.6 + FAQ); buyer VAT on B2B (How-to-Prepare + Art. 53); invoice title (How-to-Prepare); listing ≠ certification (FAQ_037); response outcomes (Detailed Guideline §10).
2. **Reword the fine table** to match the primary announcement: non-issuance from SAR 5,000; deletion/amendment from SAR 10,000; QR/buyer-VAT/malfunction warning-first; ceiling SAR 50,000. **Do NOT present the "warning → 1,000 → 5,000 → 10,000 → 40,000" ladder as the e-invoicing fine schedule** — label it as the general VAT field-control reclassification (30 Jan 2022) if included at all.
3. **Set CSID validity as "up to 5 years (60 months), per-certificate NotAfter"** citing the Security Features Standard v1.2 §2.2.2, and build renewal reminders around the certificate's embedded expiry rather than a hard-coded 1yr/3yr assumption. Implement renewal as revoke-old + issue-new.
4. **Model compliance testing as Functionality-Map-driven**: 3 documents for Standard-only, 3 for Simplified-only, 6 for both — not a fixed "12."
5. **Implement the extended-outage B2B path**: allow issuing an uncleared invoice, mark it non-VAT-deductible until cleared, retain API-log evidence, file the failure notification, and auto-resubmit for clearance on reconnection — bounded by the 15-day-from-month-end issuance window.
6. **Benchmarks that would change these answers:** a new ZATCA Wave 25 announcement (new threshold/deadline); a new version of any of the four core technical documents (watch the Educational Library page); publication of ZATCA's CA CP/CPS (would fix the CSID validity number definitively); or an updated official e-invoicing fines page enumerating a specific non-integration fine schedule.

## Caveats
- ZATCA's guidelines carry an explicit disclaimer that they are "prepared for educational purposes only... not considered mandatory... [taxpayers] are solely responsible for compliance." The binding instruments are the E-Invoicing Regulation, the Implementation Resolution and its Annexes (1) and (2), the VAT Law and VAT Implementing Regulations; the guidelines interpret these.
- The Arabic version of every ZATCA document prevails over the English translation in case of conflict.
- The "escalating fine ladder" and the specific non-integration fine amount (Jaicome: "starts at SAR 10,000 on the second violation and climbs to SAR 50,000 after the sixth") could NOT be verified against the primary e-invoicing fines announcement and should be flagged as secondary-sourced in the Rule Registry.
- CSID validity is not published as a single clean fixed figure; the "5 years / up to 60 months" value is the best-supported (Security Features Standard + ZATCA forum staff), while "1 year compliance / 3 years production" is unsupported by primary sources.
- Retry timings for outage handling (~5 min, ~15 min) are marked "TBC" in the guideline and are not binding thresholds.
- The original News_465 fines page returned a 404 on direct fetch during research (ZATCA periodically reorganizes news URLs), but its verbatim content is preserved in search-index snapshots and corroborated by Deloitte's contemporaneous reproduction of the same official announcement; verify the live URL on ZATCA's Media Center before final publication.