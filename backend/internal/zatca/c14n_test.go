package zatca

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

// Canonicalisation is tested against the rules themselves, not against our own
// output.
//
// The digest this produces is checked by ZATCA, with ZATCA's implementation. A
// test that asserted "the canonicaliser still does what it did yesterday" would
// pass forever while every invoice was rejected. So each case below encodes a
// rule from the W3C Canonical XML specification and says which one.

func canonicalOf(t *testing.T, doc string) string {
	t.Helper()
	root, err := parseXML([]byte(doc))
	if err != nil {
		t.Fatalf("parsing: %v\n%s", err, doc)
	}
	return string(canonicalize(root))
}

// §2.2: attributes are ordered by namespace URI, then by local name. An
// unprefixed attribute is in no namespace and therefore sorts first.
func TestAttributesAreSortedIntoCanonicalOrder(t *testing.T) {
	got := canonicalOf(t, `<doc b="2" a="1" c="3"></doc>`)
	if want := `<doc a="1" b="2" c="3"></doc>`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// §2.2: the namespace axis comes before the attribute axis, the default
// declaration before prefixed ones, and prefixes in lexicographic order.
func TestNamespacesAreWrittenBeforeAttributesAndInOrder(t *testing.T) {
	got := canonicalOf(t,
		`<doc xmlns:z="urn:z" attr="v" xmlns:a="urn:a" xmlns="urn:d"></doc>`)
	want := `<doc xmlns="urn:d" xmlns:a="urn:a" xmlns:z="urn:z" attr="v"></doc>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// §2.3: an empty element is written as a start tag and an end tag, never as a
// self-closing tag.
func TestAnEmptyElementIsWrittenAsAPair(t *testing.T) {
	got := canonicalOf(t, `<doc><a/><b></b></doc>`)
	if want := `<doc><a></a><b></b></doc>`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// §2.3: in a text node, &, < and > are escaped and the quote characters are
// NOT. Escaping an apostrophe here is the classic way to produce bytes no other
// implementation agrees with.
func TestTextEscapingFollowsTheSpecificationExactly(t *testing.T) {
	got := canonicalOf(t, `<doc>a &amp; b &lt; c &gt; d " e ' f</doc>`)
	if want := `<doc>a &amp; b &lt; c &gt; d " e ' f</doc>`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// §2.3: in an attribute value, the greater-than sign is NOT escaped, and tab,
// newline and carriage return ARE. This is the opposite of the text rule for >.
func TestAttributeEscapingDiffersFromTextEscaping(t *testing.T) {
	got := canonicalOf(t, `<doc a="x &gt; y &lt; z &amp; w &quot;q&quot;"></doc>`)
	want := `<doc a="x > y &lt; z &amp; w &quot;q&quot;"></doc>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// §2.3: a declaration identical to the one already in force is superfluous and
// is not written again.
func TestARedundantNamespaceDeclarationIsDropped(t *testing.T) {
	got := canonicalOf(t,
		`<doc xmlns:a="urn:a"><child xmlns:a="urn:a"><leaf/></child></doc>`)
	want := `<doc xmlns:a="urn:a"><child><leaf></leaf></child></doc>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// A declaration that CHANGES the binding is not superfluous and must survive.
func TestARebindingNamespaceDeclarationIsKept(t *testing.T) {
	got := canonicalOf(t,
		`<doc xmlns:a="urn:one"><child xmlns:a="urn:two"></child></doc>`)
	want := `<doc xmlns:a="urn:one"><child xmlns:a="urn:two"></child></doc>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// Inclusive canonicalisation renders every namespace in scope on the apex,
// whether or not the subtree uses it. Exclusive c14n does the opposite, and
// c14n11 — which ZATCA names — is the inclusive one.
func TestTheApexCarriesEveryNamespaceInScope(t *testing.T) {
	got := canonicalOf(t,
		`<doc xmlns:unused="urn:unused" xmlns:used="urn:used"><used:x/></doc>`)
	want := `<doc xmlns:unused="urn:unused" xmlns:used="urn:used"><used:x></used:x></doc>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// The XML declaration is not part of the canonical form.
func TestTheXMLDeclarationIsDropped(t *testing.T) {
	got := canonicalOf(t, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<doc></doc>")
	if want := `<doc></doc>`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// Line endings are normalised before anything else sees them.
func TestLineEndingsAreNormalised(t *testing.T) {
	got := canonicalOf(t, "<doc>a\r\nb\rc</doc>")
	if want := "<doc>a\nb\nc</doc>"; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// CDATA is replaced by its character content, then escaped as text.
func TestCDATABecomesOrdinaryText(t *testing.T) {
	got := canonicalOf(t, `<doc><![CDATA[a < b & c]]></doc>`)
	if want := `<doc>a &lt; b &amp; c</doc>`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// Whitespace between elements is significant and survives.
func TestWhitespaceBetweenElementsIsPreserved(t *testing.T) {
	got := canonicalOf(t, "<doc>\n  <a>1</a>\n  <b>2</b>\n</doc>")
	if want := "<doc>\n  <a>1</a>\n  <b>2</b>\n</doc>"; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// Comments, processing instructions and DOCTYPE are refused rather than
// canonicalised wrongly. This package never emits them.
func TestConstructsThisPackageWillNotCanonicaliseAreRefused(t *testing.T) {
	for _, doc := range []string{
		`<doc><!-- a comment --></doc>`,
		`<doc><?target data?></doc>`,
		`<!DOCTYPE doc><doc></doc>`,
	} {
		if _, err := parseXML([]byte(doc)); err == nil {
			t.Errorf("this was accepted and should not have been: %s", doc)
		}
	}
}

// --- the transform chain --------------------------------------------------------

// §2.3.3 removes three subtrees before the digest is taken, because all three
// are written into the document AFTER the digest exists.
func TestTheTransformChainRemovesExactlyTheThreeSubtrees(t *testing.T) {
	const doc = `<Invoice xmlns="urn:i" xmlns:cac="urn:cac" xmlns:cbc="urn:cbc" xmlns:ext="urn:ext">` +
		`<ext:UBLExtensions><ext:UBLExtension>signature</ext:UBLExtension></ext:UBLExtensions>` +
		`<cbc:ID>INV-1</cbc:ID>` +
		`<cac:AdditionalDocumentReference><cbc:ID>ICV</cbc:ID><cbc:UUID>7</cbc:UUID></cac:AdditionalDocumentReference>` +
		`<cac:AdditionalDocumentReference><cbc:ID>QR</cbc:ID><cbc:UUID>zzz</cbc:UUID></cac:AdditionalDocumentReference>` +
		`<cac:Signature><cbc:ID>sig</cbc:ID></cac:Signature>` +
		`<cbc:Total>10.00</cbc:Total>` +
		`</Invoice>`

	out, err := CanonicalInvoice([]byte(doc))
	if err != nil {
		t.Fatalf("canonicalising: %v", err)
	}
	got := string(out)

	for _, gone := range []string{"UBLExtensions", "cac:Signature", "zzz"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived the transform chain:\n%s", gone, got)
		}
	}
	// The ICV reference is NOT removed — only the QR one is.
	for _, kept := range []string{"INV-1", "ICV", "10.00"} {
		if !strings.Contains(got, kept) {
			t.Errorf("%q was removed and should not have been:\n%s", kept, got)
		}
	}
}

// Removing an element must not disturb the whitespace around it. The XPath
// transforms delete ELEMENTS; the text between them is nobody's descendant and
// survives, and a canonicaliser that tidied it up would change the digest.
func TestRemovingASubtreeLeavesTheSurroundingWhitespaceAlone(t *testing.T) {
	const doc = "<Invoice xmlns:cac=\"urn:cac\" xmlns:cbc=\"urn:cbc\">\n" +
		"  <cbc:ID>A</cbc:ID>\n" +
		"  <cac:Signature><cbc:ID>s</cbc:ID></cac:Signature>\n" +
		"  <cbc:Total>1</cbc:Total>\n" +
		"</Invoice>"

	out, err := CanonicalInvoice([]byte(doc))
	if err != nil {
		t.Fatalf("canonicalising: %v", err)
	}
	want := "<Invoice xmlns:cac=\"urn:cac\" xmlns:cbc=\"urn:cbc\">\n" +
		"  <cbc:ID>A</cbc:ID>\n" +
		"  \n" +
		"  <cbc:Total>1</cbc:Total>\n" +
		"</Invoice>"
	if string(out) != want {
		t.Errorf("got  %q\nwant %q", out, want)
	}
}

// The same document canonicalises to the same bytes however its attributes and
// namespace declarations happened to be written. That is the whole purpose.
func TestTwoSpellingsOfTheSameDocumentAgree(t *testing.T) {
	a := canonicalOf(t, `<doc xmlns:x="urn:x" p="1" q="2"><x:c/></doc>`)
	b := canonicalOf(t, `<doc q="2" xmlns:x="urn:x" p="1"><x:c></x:c></doc>`)
	if a != b {
		t.Errorf("the same document canonicalised two ways:\n%s\n%s", a, b)
	}
}

// The digest is over the canonical bytes, and is stable.
func TestTheInvoiceDigestIsStableAcrossSpellings(t *testing.T) {
	digest := func(doc string) string {
		out, err := CanonicalInvoice([]byte(doc))
		if err != nil {
			t.Fatalf("canonicalising: %v", err)
		}
		sum := sha256.Sum256(out)
		return base64.StdEncoding.EncodeToString(sum[:])
	}

	one := digest(`<Invoice xmlns:cbc="urn:cbc" a="1" b="2"><cbc:ID>X</cbc:ID></Invoice>`)
	two := digest(`<Invoice b="2" xmlns:cbc="urn:cbc" a="1"><cbc:ID>X</cbc:ID></Invoice>`)
	if one != two {
		t.Errorf("digests differ for the same document: %s vs %s", one, two)
	}
	if len(one) != 44 {
		t.Errorf("a base64 SHA-256 is 44 characters, got %d", len(one))
	}
}
