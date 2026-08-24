package zatca

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mahedi-emon/rawsyst-pos/backend/internal/platform/errs"
)

// Canonical XML 1.1, and the transform chain ZATCA hashes through.
//
// # Why this is hand-written
//
// Go has no canonicalisation at all, and encoding/xml cannot be borrowed for it:
// its decoder resolves a name to {namespace URI, local} and DISCARDS the prefix.
// Canonical XML preserves prefixes exactly — `<cbc:ID>` and `<x:ID>` bound to
// the same URI are different bytes and therefore different digests — so a
// parser that forgets them cannot be used to produce one.
//
// So this file carries a small XML reader that keeps prefixes, and a serialiser
// that implements the parts of C14N 1.1 this document set reaches.
//
// # Why it has to be exact
//
// The digest is not checked by us. ZATCA receives the signed document, applies
// the same transforms, canonicalises with its own implementation and compares.
// A single byte of difference — an attribute in the wrong order, a namespace
// declared in the wrong place, `<a/>` where `<a></a>` belongs — produces a
// different SHA-256 and the invoice is rejected with an error that says nothing
// about which byte.
//
// # What is implemented, and what is deliberately refused
//
// Implemented: elements, attributes, namespace declarations, text, CDATA (as
// text), the empty-element rule, attribute and text escaping, attribute
// ordering, and inclusive namespace rendering.
//
// Refused: comments, processing instructions and DOCTYPE. Canonical XML has
// rules for all three; this package never emits them, and a document arriving
// with one is more likely to be something we did not build than something we
// should quietly sign. Refusing is safer than canonicalising them wrongly.

// --- a document tree that remembers prefixes ---------------------------------

type xmlNode interface{ isNode() }

type xmlText struct{ data string }

func (xmlText) isNode() {}

type xmlAttr struct {
	prefix string // "" for an unprefixed attribute, "xmlns" for a declaration
	local  string
	value  string
}

type xmlElement struct {
	prefix   string
	local    string
	attrs    []xmlAttr
	children []xmlNode

	// removed marks a subtree the transform chain deletes. The node stays in
	// the tree so its siblings' whitespace is untouched, which matters: the
	// XPath transforms remove ELEMENTS, and the text between them is not a
	// descendant of anything removed, so it survives.
	removed bool
}

func (*xmlElement) isNode() {}

// name renders the qualified name as it was written.
func (e *xmlElement) name() string {
	if e.prefix == "" {
		return e.local
	}
	return e.prefix + ":" + e.local
}

func (a xmlAttr) name() string {
	if a.prefix == "" {
		return a.local
	}
	return a.prefix + ":" + a.local
}

// isNamespace reports whether the attribute declares a namespace.
func (a xmlAttr) isNamespace() bool {
	return a.prefix == "xmlns" || (a.prefix == "" && a.local == "xmlns")
}

// nsPrefix is the prefix a declaration binds, "" for the default namespace.
func (a xmlAttr) nsPrefix() string {
	if a.prefix == "xmlns" {
		return a.local
	}
	return ""
}

// --- reading ------------------------------------------------------------------

type xmlReader struct {
	src string
	at  int
}

// parseXML builds the tree, preserving prefixes and document order.
func parseXML(doc []byte) (*xmlElement, error) {
	r := &xmlReader{src: string(doc)}
	return r.document()
}

func (r *xmlReader) fail(what string) error {
	return errs.Newf(errs.CodeInvalidInput,
		"This document could not be read as XML: %s at byte %d.", what, r.at)
}

func (r *xmlReader) document() (*xmlElement, error) {
	var root *xmlElement

	for r.at < len(r.src) {
		if !strings.HasPrefix(r.src[r.at:], "<") {
			// Whitespace between the declaration and the root is discarded by
			// canonicalisation of a document subset rooted at the element.
			r.skipSpace()
			if r.at >= len(r.src) {
				break
			}
			if !strings.HasPrefix(r.src[r.at:], "<") {
				return nil, r.fail("text outside the document element")
			}
			continue
		}

		switch {
		case strings.HasPrefix(r.src[r.at:], "<?"):
			end := strings.Index(r.src[r.at:], "?>")
			if end < 0 {
				return nil, r.fail("an unterminated declaration")
			}
			// The XML declaration is dropped by canonicalisation. Any OTHER
			// processing instruction is refused rather than mishandled.
			if !strings.HasPrefix(r.src[r.at:], "<?xml ") &&
				!strings.HasPrefix(r.src[r.at:], "<?xml?") {
				return nil, r.fail("a processing instruction, which this package refuses")
			}
			r.at += end + 2

		case strings.HasPrefix(r.src[r.at:], "<!--"):
			return nil, r.fail("a comment, which this package refuses")

		case strings.HasPrefix(r.src[r.at:], "<!"):
			return nil, r.fail("a DOCTYPE or declaration, which this package refuses")

		default:
			if root != nil {
				return nil, r.fail("a second document element")
			}
			e, err := r.element()
			if err != nil {
				return nil, err
			}
			root = e
		}
	}

	if root == nil {
		return nil, r.fail("no document element")
	}
	return root, nil
}

func (r *xmlReader) skipSpace() {
	for r.at < len(r.src) {
		switch r.src[r.at] {
		case ' ', '\t', '\r', '\n':
			r.at++
		default:
			return
		}
	}
}

func isNameByte(b byte) bool {
	return b == ':' || b == '_' || b == '-' || b == '.' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b >= 0x80
}

func (r *xmlReader) name() (prefix, local string, err error) {
	start := r.at
	for r.at < len(r.src) && isNameByte(r.src[r.at]) {
		r.at++
	}
	if r.at == start {
		return "", "", r.fail("an empty name")
	}
	qname := r.src[start:r.at]
	if i := strings.IndexByte(qname, ':'); i >= 0 {
		return qname[:i], qname[i+1:], nil
	}
	return "", qname, nil
}

// element reads one element and everything inside it.
func (r *xmlReader) element() (*xmlElement, error) {
	if r.src[r.at] != '<' {
		return nil, r.fail("an element that does not start with '<'")
	}
	r.at++

	prefix, local, err := r.name()
	if err != nil {
		return nil, err
	}
	e := &xmlElement{prefix: prefix, local: local}

	for {
		r.skipSpace()
		if r.at >= len(r.src) {
			return nil, r.fail("an unterminated start tag")
		}
		if strings.HasPrefix(r.src[r.at:], "/>") {
			r.at += 2
			return e, nil
		}
		if r.src[r.at] == '>' {
			r.at++
			break
		}

		ap, al, err := r.name()
		if err != nil {
			return nil, err
		}
		r.skipSpace()
		if r.at >= len(r.src) || r.src[r.at] != '=' {
			return nil, r.fail("an attribute with no value")
		}
		r.at++
		r.skipSpace()
		if r.at >= len(r.src) || (r.src[r.at] != '"' && r.src[r.at] != '\'') {
			return nil, r.fail("an unquoted attribute value")
		}
		quote := r.src[r.at]
		r.at++
		start := r.at
		for r.at < len(r.src) && r.src[r.at] != quote {
			r.at++
		}
		if r.at >= len(r.src) {
			return nil, r.fail("an unterminated attribute value")
		}
		raw := r.src[start:r.at]
		r.at++

		e.attrs = append(e.attrs, xmlAttr{prefix: ap, local: al, value: unescapeXML(raw)})
	}

	// Children, until the matching end tag.
	for {
		if r.at >= len(r.src) {
			return nil, r.fail("an unterminated element")
		}

		if strings.HasPrefix(r.src[r.at:], "</") {
			r.at += 2
			ep, el, err := r.name()
			if err != nil {
				return nil, err
			}
			r.skipSpace()
			if r.at >= len(r.src) || r.src[r.at] != '>' {
				return nil, r.fail("an unterminated end tag")
			}
			r.at++
			if ep != e.prefix || el != e.local {
				return nil, r.fail("mismatched tags")
			}
			return e, nil
		}

		switch {
		case strings.HasPrefix(r.src[r.at:], "<!--"):
			return nil, r.fail("a comment, which this package refuses")
		case strings.HasPrefix(r.src[r.at:], "<![CDATA["):
			end := strings.Index(r.src[r.at:], "]]>")
			if end < 0 {
				return nil, r.fail("an unterminated CDATA section")
			}
			// Canonical XML replaces CDATA with its character content.
			e.children = append(e.children,
				xmlText{data: r.src[r.at+len("<![CDATA[") : r.at+end]})
			r.at += end + 3
		case strings.HasPrefix(r.src[r.at:], "<?"):
			return nil, r.fail("a processing instruction, which this package refuses")
		case strings.HasPrefix(r.src[r.at:], "<!"):
			return nil, r.fail("a declaration, which this package refuses")
		case r.src[r.at] == '<':
			child, err := r.element()
			if err != nil {
				return nil, err
			}
			e.children = append(e.children, child)
		default:
			start := r.at
			for r.at < len(r.src) && r.src[r.at] != '<' {
				r.at++
			}
			e.children = append(e.children, xmlText{data: unescapeXML(r.src[start:r.at])})
		}
	}
}

// unescapeXML resolves the five predefined entities and numeric references.
func unescapeXML(s string) string {
	if !strings.ContainsAny(s, "&\r") {
		return s
	}
	// Line endings are normalised to #xA before anything else, per the XML
	// specification's end-of-line handling.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if !strings.Contains(s, "&") {
		return s
	}

	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i:], ';')
		if end < 0 {
			b.WriteByte(s[i])
			i++
			continue
		}
		entity := s[i+1 : i+end]
		switch entity {
		case "amp":
			b.WriteByte('&')
		case "lt":
			b.WriteByte('<')
		case "gt":
			b.WriteByte('>')
		case "quot":
			b.WriteByte('"')
		case "apos":
			b.WriteByte('\'')
		default:
			if strings.HasPrefix(entity, "#") {
				var r rune
				var n int
				if strings.HasPrefix(entity, "#x") || strings.HasPrefix(entity, "#X") {
					n, _ = fmt.Sscanf(entity[2:], "%x", &r)
				} else {
					n, _ = fmt.Sscanf(entity[1:], "%d", &r)
				}
				if n == 1 {
					b.WriteRune(r)
				}
			}
		}
		i += end + 1
	}
	return b.String()
}

// --- canonical serialisation ---------------------------------------------------

// escapeText applies the text-node rules of Canonical XML.
//
// Only these three, plus the carriage return. A canonicaliser that also escaped
// the apostrophe or the quote in a text node would produce bytes that differ
// from every other implementation's.
var textEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	"\r", "&#xD;",
)

// escapeAttr applies the attribute-value rules, which differ from text: the
// greater-than sign is NOT escaped, and tab, newline and return are.
var attrEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	`"`, "&quot;",
	"\t", "&#x9;",
	"\n", "&#xA;",
	"\r", "&#xD;",
)

// namespaceScope tracks which prefix is bound to which URI, and which bindings
// have already been written out.
type namespaceScope struct {
	bound    map[string]string // prefix -> URI, "" is the default namespace
	rendered map[string]string // prefix -> URI as last written
}

func (s namespaceScope) clone() namespaceScope {
	out := namespaceScope{
		bound:    make(map[string]string, len(s.bound)),
		rendered: make(map[string]string, len(s.rendered)),
	}
	for k, v := range s.bound {
		out.bound[k] = v
	}
	for k, v := range s.rendered {
		out.rendered[k] = v
	}
	return out
}

// canonicalize renders the tree per Canonical XML 1.1, skipping removed nodes.
//
// Inclusive canonicalisation, which is what http://www.w3.org/2006/12/xml-c14n11
// names: the apex element carries every namespace in scope, whether or not the
// subtree uses it. That is the opposite of exclusive c14n, and choosing wrongly
// is the classic way to produce a signature nobody can verify.
func canonicalize(root *xmlElement) []byte {
	var b strings.Builder
	scope := namespaceScope{
		bound:    map[string]string{},
		rendered: map[string]string{},
	}
	writeElement(&b, root, scope, true)
	return []byte(b.String())
}

func writeElement(b *strings.Builder, e *xmlElement, parent namespaceScope, apex bool) {
	scope := parent.clone()

	// Bind whatever this element declares.
	for _, a := range e.attrs {
		if a.isNamespace() {
			scope.bound[a.nsPrefix()] = a.value
		}
	}

	b.WriteByte('<')
	b.WriteString(e.name())

	// Namespace axis first, sorted with the default namespace ahead of the
	// prefixed ones and the rest in lexicographic prefix order.
	var prefixes []string
	if apex {
		for p := range scope.bound {
			prefixes = append(prefixes, p)
		}
	} else {
		for _, a := range e.attrs {
			if a.isNamespace() {
				prefixes = append(prefixes, a.nsPrefix())
			}
		}
	}
	sort.Slice(prefixes, func(i, j int) bool {
		if prefixes[i] == "" {
			return prefixes[j] != ""
		}
		if prefixes[j] == "" {
			return false
		}
		return prefixes[i] < prefixes[j]
	})

	for _, p := range prefixes {
		uri := scope.bound[p]
		// A declaration identical to the one already in force is superfluous
		// and is not written again.
		if was, ok := scope.rendered[p]; ok && was == uri {
			continue
		}
		// An empty default declaration where none is in force adds nothing.
		if p == "" && uri == "" {
			if _, ok := scope.rendered[""]; !ok {
				continue
			}
		}
		b.WriteString(" xmlns")
		if p != "" {
			b.WriteByte(':')
			b.WriteString(p)
		}
		b.WriteString(`="`)
		b.WriteString(attrEscaper.Replace(uri))
		b.WriteByte('"')
		scope.rendered[p] = uri
	}

	// Then the attribute axis, sorted by namespace URI and then local name.
	// An unprefixed attribute is in no namespace, so it sorts first.
	var plain []xmlAttr
	for _, a := range e.attrs {
		if !a.isNamespace() {
			plain = append(plain, a)
		}
	}
	sort.Slice(plain, func(i, j int) bool {
		ui, uj := scope.bound[plain[i].prefix], scope.bound[plain[j].prefix]
		if plain[i].prefix == "" {
			ui = ""
		}
		if plain[j].prefix == "" {
			uj = ""
		}
		if ui != uj {
			return ui < uj
		}
		return plain[i].local < plain[j].local
	})
	for _, a := range plain {
		b.WriteByte(' ')
		b.WriteString(a.name())
		b.WriteString(`="`)
		b.WriteString(attrEscaper.Replace(a.value))
		b.WriteByte('"')
	}

	b.WriteByte('>')

	for _, child := range e.children {
		switch c := child.(type) {
		case xmlText:
			b.WriteString(textEscaper.Replace(c.data))
		case *xmlElement:
			if c.removed {
				continue
			}
			writeElement(b, c, scope, false)
		}
	}

	// Never a self-closing tag: Canonical XML writes the pair even when empty.
	b.WriteString("</")
	b.WriteString(e.name())
	b.WriteByte('>')
}

// --- the transform chain --------------------------------------------------------

// markInvoiceTransforms deletes the three subtrees §2.3.3 removes before hashing.
//
// The XPath expressions in canonical.go say the same thing declaratively:
//
//	not(//ancestor-or-self::ext:UBLExtensions)
//	not(//ancestor-or-self::cac:Signature)
//	not(//ancestor-or-self::cac:AdditionalDocumentReference[cbc:ID='QR'])
//
// All three are written INTO the document after the digest exists, so hashing
// them would be circular. Matched on local name so a document using different
// prefixes for the same namespaces is treated the same way.
func markInvoiceTransforms(root *xmlElement) {
	var walk func(e *xmlElement)
	walk = func(e *xmlElement) {
		for _, child := range e.children {
			c, ok := child.(*xmlElement)
			if !ok {
				continue
			}
			switch c.local {
			case "UBLExtensions", "Signature":
				c.removed = true
			case "AdditionalDocumentReference":
				if childText(c, "ID") == "QR" {
					c.removed = true
				}
			}
			if !c.removed {
				walk(c)
			}
		}
	}
	walk(root)
}

// childText returns the text of the first child element with this local name.
func childText(e *xmlElement, local string) string {
	for _, child := range e.children {
		c, ok := child.(*xmlElement)
		if !ok || c.local != local {
			continue
		}
		var b strings.Builder
		for _, gc := range c.children {
			if t, ok := gc.(xmlText); ok {
				b.WriteString(t.data)
			}
		}
		return strings.TrimSpace(b.String())
	}
	return ""
}

// CanonicalInvoice applies the transform chain and returns the bytes that are
// hashed — the exact input to SHA-256 for both the invoice digest and the PIH.
func CanonicalInvoice(doc []byte) ([]byte, error) {
	root, err := parseXML(doc)
	if err != nil {
		return nil, err
	}
	markInvoiceTransforms(root)
	return canonicalize(root), nil
}

// canonicalizeInContext canonicalises one element as it sits in its document.
//
// This is the difference between a signature that verifies and one that does
// not, and it is the classic way to get XAdES wrong.
//
// c14n11 is INCLUSIVE: the apex of a canonicalised subset carries every
// namespace in scope, not merely the ones its subtree uses. So when ZATCA
// verifies, it parses the whole invoice, finds ds:SignedInfo, and canonicalises
// it with the Invoice element's xmlns, cac, cbc, ext, sig, sac, sbc and ds
// declarations all rendered on it.
//
// Canonicalising the same fragment on its own would render only the namespaces
// written on the fragment — a different byte string, a different digest, and a
// signature that verifies against nothing. Hence this: find the element in the
// parsed document, gather what its ancestors bind, and canonicalise with that
// in scope.
func canonicalizeInContext(root *xmlElement, match func(*xmlElement) bool) ([]byte, error) {
	bound := map[string]string{}

	var found *xmlElement
	var foundScope map[string]string

	var walk func(e *xmlElement, inherited map[string]string)
	walk = func(e *xmlElement, inherited map[string]string) {
		if found != nil {
			return
		}

		scope := make(map[string]string, len(inherited)+len(e.attrs))
		for k, v := range inherited {
			scope[k] = v
		}
		for _, a := range e.attrs {
			if a.isNamespace() {
				scope[a.nsPrefix()] = a.value
			}
		}

		if match(e) {
			found = e
			// The apex renders what its ANCESTORS bind; its own declarations
			// are already among its attributes and are rendered from there.
			foundScope = inherited
			return
		}

		for _, child := range e.children {
			if c, ok := child.(*xmlElement); ok {
				walk(c, scope)
			}
		}
	}
	walk(root, bound)

	if found == nil {
		return nil, errs.New(errs.CodeInternal,
			"That element is not in the document, so it cannot be canonicalised.")
	}

	var b strings.Builder
	writeElement(&b, found, namespaceScope{
		bound:    foundScope,
		rendered: map[string]string{},
	}, true)
	return []byte(b.String()), nil
}

// elementWithID matches an element carrying this Id attribute.
func elementWithID(id string) func(*xmlElement) bool {
	return func(e *xmlElement) bool {
		for _, a := range e.attrs {
			if a.prefix == "" && a.local == "Id" && a.value == id {
				return true
			}
		}
		return false
	}
}

// elementNamed matches by local name, ignoring the prefix.
func elementNamed(local string) func(*xmlElement) bool {
	return func(e *xmlElement) bool { return e.local == local }
}
