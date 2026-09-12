// Turning a published document into text a machine can read a figure out of.
//
// # Why this is separate from the extraction
//
// Two different problems wear the same coat. "What does this PDF say" is a
// decoding problem with one right answer. "Which of those words is the
// entitlement" is a reading problem, and a reading can be wrong in ways a
// decoder cannot. Keeping them apart means the reading in `extract.go` works on
// one normalised shape and never has to know whether it came from a PDF, an
// HTML page or a plain-text file, and means the normalisation can be tested on
// its own against text nobody is trying to interpret.
//
// # What normalisation does, and why each step is needed
//
// The Ministry publishes the Labour Law as a PDF laid out in two columns with
// justified text. Extracted, Article 84 arrives looking like this:
//
//	an end
//	-
//	of
//	-
//	service award equivalent to the
//	amount of a half
//	-
//	month wage for each of the first five years
//
// with a running "Page 22" header dropped into the middle of a sentence and a
// curly apostrophe in "worker's". None of that is meaningful; all of it defeats
// a pattern written against the sentence as printed. So:
//
//   - typographic characters are folded to their ASCII equivalents, because a
//     regex written with ' should match a document set with ’
//   - running page furniture is removed, because it interrupts sentences
//   - whitespace is collapsed to single spaces, because line breaks in a PDF
//     are a property of the column width and nothing else
//   - hyphens lose the space either side, which reassembles every word the
//     layout broke across a line
//
// The result is the sentence as a person reads it, which is what the evidence
// stored beside each extracted figure has to be. An operator checking the
// software's reading against the document must see the sentence, not a slice of
// PDF internals.
package registry

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"

	"github.com/mahedi-emon/Biz1core/backend/internal/platform/errs"
)

// MaxSourceBytes is the largest document this product will hold as evidence.
//
// The Labour Law is a third of a megabyte. Eight is room for a regulation an
// order of magnitude larger and a refusal well before anything that would
// trouble a request, a row or an 8 GB machine.
const MaxSourceBytes = 8 << 20

// DecodeSource turns a retrieved document into readable text.
//
// The media type decides how, and an unrecognised one is refused rather than
// guessed at: handing PDF bytes to an HTML stripper produces text that looks
// like text and says nothing, which is the worst of the available outcomes
// because it fails silently at the reading stage instead of loudly here.
func DecodeSource(mediaType string, content []byte) (string, error) {
	if len(content) == 0 {
		return "", errs.New(errs.CodeInvalidInput,
			"That document is empty.")
	}
	switch base := mediaTypeBase(mediaType); base {
	case "application/pdf":
		return decodePDF(content)
	case "text/html", "application/xhtml+xml":
		return stripHTML(string(content)), nil
	case "text/plain", "text/markdown":
		return string(content), nil
	default:
		return "", errs.Newf(errs.CodeInvalidInput,
			"This product can read a regulatory source published as PDF, "+
				"HTML or plain text. That document says it is %q. If the "+
				"authority publishes the same document in one of those, use "+
				"that address; a format nobody can read is not evidence.",
			base)
	}
}

// mediaTypeBase drops the parameters, so `text/html; charset=utf-8` is HTML.
func mediaTypeBase(mediaType string) string {
	base, _, _ := strings.Cut(mediaType, ";")
	return strings.ToLower(strings.TrimSpace(base))
}

// SniffMediaType names what a document actually is, from its own first bytes.
//
// A server's Content-Type is a claim, and government file servers are not
// famous for the accuracy of theirs. The bytes are not a claim.
func SniffMediaType(content []byte, declared string) string {
	switch {
	case bytes.HasPrefix(content, []byte("%PDF-")):
		return "application/pdf"
	case looksLikeHTML(content):
		return "text/html"
	case looksLikeText(content):
		return "text/plain"
	}
	if base := mediaTypeBase(declared); base != "" {
		return base
	}
	return "application/octet-stream"
}

// looksLikeText says the bytes are readable prose rather than a binary format.
//
// Worth sniffing rather than trusting the declaration, because the declaration
// is frequently `application/octet-stream` — that is what a browser's multipart
// encoder falls back to, and what a file server says about anything it has no
// mapping for. Refusing a perfectly readable regulation because the thing that
// sent it did not know what it was called would be a refusal about plumbing
// dressed as a refusal about evidence.
//
// Valid UTF-8, and no control characters outside the ones text is made of. A
// PDF and a ZIP both fail on the first NUL within a page of the start; nothing
// that survives this is unreadable in a way that matters.
func looksLikeText(content []byte) bool {
	head := content
	if len(head) > 4096 {
		head = head[:4096]
	}
	if !utf8.Valid(head) {
		return false
	}
	for _, r := range string(head) {
		switch r {
		case '\n', '\r', '\t', '\f', '\v':
			continue
		}
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func looksLikeHTML(content []byte) bool {
	head := content
	if len(head) > 1024 {
		head = head[:1024]
	}
	lower := bytes.ToLower(head)
	return bytes.Contains(lower, []byte("<!doctype html")) ||
		bytes.Contains(lower, []byte("<html"))
}

// decodePDF reads every page's text in order.
//
// A page that will not render is skipped rather than fatal: a scanned annexe or
// a malformed embedded font at the back of a document must not make the
// articles at the front unreadable. If the whole document yields nothing, that
// is reported — a PDF of page images has no text layer, and telling somebody
// that plainly is better than extracting zero figures from it and blaming the
// patterns.
func decodePDF(content []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", errs.Wrap(err, errs.CodeInvalidInput,
			"That file could not be read as a PDF.")
	}

	var b strings.Builder
	pages := r.NumPage()
	for i := 1; i <= pages; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, e := p.GetPlainText(nil)
		if e != nil {
			continue
		}
		b.WriteString(text)
		b.WriteString("\n")
	}

	out := b.String()
	if strings.TrimSpace(out) == "" {
		return "", errs.New(errs.CodeInvalidInput,
			"That PDF holds no text — it is page images, or its text is not "+
				"extractable. A document nothing can read cannot support a "+
				"legal value. Use the authority's text or HTML publication "+
				"of the same document if there is one.")
	}
	return out, nil
}

var (
	// Written as an alternation rather than with a back-reference: Go's regexp
	// is RE2 and has none, which is the price of never backtracking.
	htmlScriptOrStyle = regexp.MustCompile(
		`(?is)<script\b[^>]*>.*?</script>|<style\b[^>]*>.*?</style>`)
	htmlTag    = regexp.MustCompile(`(?s)<[^>]*>`)
	htmlEntity = strings.NewReplacer(
		"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", "\"", "&#39;", "'", "&rsquo;", "'", "&lsquo;", "'",
		"&ldquo;", "\"", "&rdquo;", "\"", "&mdash;", "-", "&ndash;", "-",
	)
)

// stripHTML reduces a page to the words on it.
//
// Deliberately crude. This is not a browser and it is not trying to be one: it
// removes the parts of an HTML document that are never the law — script, style
// and markup — and leaves the text. Anything cleverer would be a rendering
// engine's job, and a rendering engine's bugs would become this product's
// reading of a statute.
func stripHTML(s string) string {
	s = htmlScriptOrStyle.ReplaceAllString(s, " ")
	s = htmlTag.ReplaceAllString(s, " ")
	return htmlEntity.Replace(s)
}

var (
	// Page furniture: a running header or footer that a PDF drops into the
	// middle of whatever sentence happens to be crossing the page break.
	pageFurniture = regexp.MustCompile(`(?i)\bpage\s+\d{1,4}\b`)

	// Whitespace of every kind, including the non-breaking space that HTML
	// tables are held together with.
	anyWhitespace = regexp.MustCompile(`[\s\p{Zs}]+`)

	// A hyphen with optional space either side, which is how a justified PDF
	// renders a word broken across a line.
	looseHyphen = regexp.MustCompile(`\s*-\s*`)

	typography = strings.NewReplacer(
		"‘", "'", "’", "'", // curly single quotes
		"“", "\"", "”", "\"", // curly double quotes
		"–", "-", "—", "-", "−", "-", // en, em, minus
		" ", " ", // non-breaking space
		"\u200b", "", "\ufeff", "", // zero width space, byte order mark
		"­", "", // soft hyphen
	)
)

// NormaliseSource turns decoded text into the sentences a person reads.
//
// The output is what gets stored as evidence beside every extracted figure, so
// it has to be legible on its own. It is also what every pattern in
// `extract.go` is written against, so it has to be stable: the same document
// fetched twice must normalise identically, or a refresh would report a change
// that is really a whitespace difference.
func NormaliseSource(text string) string {
	text = typography.Replace(text)

	// Control characters a PDF text layer can carry, other than the whitespace
	// the next step handles.
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r == '\r' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)

	text = pageFurniture.ReplaceAllString(text, " ")
	text = anyWhitespace.ReplaceAllString(text, " ")
	text = looseHyphen.ReplaceAllString(text, "-")
	return strings.TrimSpace(text)
}

// Scanned is a passage held in two forms at once.
//
// # The problem it exists for
//
// A PDF's text layer records glyphs and their positions, not words. Where the
// Ministry's typesetter kerned a pair of letters apart, the extracted text
// carries a space inside the word:
//
//	"due to the worker's resignat ion, he shall"
//	"and to the ful l award if his service amounts to 10 years or more"
//
// Collapsing whitespace cannot fix that — the space is between two letters of
// one word and looks exactly like the space between two words. Nor can it be
// repaired by guessing, because "ful l" and "for m" are indistinguishable from
// legitimate short words without a dictionary, and a dictionary that rewrote a
// statute before reading it would be a worse problem than the one it solved.
//
// # What it does instead
//
// It keeps the passage twice: as a person reads it, and with every space
// removed. Patterns match against the spaceless form, where "the ful l award"
// and "the full award" are the same string, and every match is mapped back to
// the readable form so the evidence quoted beside a figure is a sentence
// rather than a run-on. Nothing is rewritten and nothing is guessed; the same
// characters are simply read in an order that a line break cannot disturb.
type Scanned struct {
	// Text is the passage as it reads.
	Text string

	// Dense is the same characters with the spaces taken out.
	Dense string

	// at[i] is where Dense[i] sits in Text.
	at []int
}

// Scan prepares a normalised passage for reading.
func Scan(text string) Scanned {
	s := Scanned{Text: text, at: make([]int, 0, len(text))}
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		if text[i] == ' ' {
			continue
		}
		b.WriteByte(text[i])
		s.at = append(s.at, i)
	}
	s.Dense = b.String()
	return s
}

// Find runs a pattern against the spaceless form.
//
// The returned indices are into Dense. Use Sentence to turn one into the
// evidence a person reads.
func (s Scanned) Find(re *regexp.Regexp) []int {
	return re.FindStringSubmatchIndex(s.Dense)
}

// At returns the text of a Dense range.
func (s Scanned) At(from, to int) string {
	if from < 0 || to > len(s.Dense) || from > to {
		return ""
	}
	return s.Dense[from:to]
}

// Sentence is the readable sentence a Dense match sits in.
func (s Scanned) Sentence(from, to int) string {
	if len(s.at) == 0 || from < 0 || from >= len(s.at) {
		return ""
	}
	start := s.at[from]
	length := 1
	if to > from && to <= len(s.at) {
		length = s.at[to-1] - start + 1
	}
	return SentenceAround(s.Text, start, length)
}

// Slice narrows to a Dense range, keeping both forms in step.
func (s Scanned) Slice(from, to int) Scanned {
	if from < 0 {
		from = 0
	}
	if to > len(s.Dense) || to < 0 {
		to = len(s.Dense)
	}
	if from > to || len(s.at) == 0 {
		return Scanned{}
	}
	textFrom := s.at[from]
	textTo := len(s.Text)
	if to < len(s.at) {
		textTo = s.at[to]
	}
	return Scan(strings.TrimSpace(s.Text[textFrom:textTo]))
}

// DensePattern compiles a pattern written with spaces for readability, against
// text that has had its spaces removed.
//
// Every space in the pattern source is dropped before compiling, so the pattern
// can be written the way the sentence reads and still match a passage a PDF
// broke in the middle of a word. It follows that a pattern passed here must
// never rely on a literal space or on `\s`: there are none left to match.
func DensePattern(pattern string) *regexp.Regexp {
	return regexp.MustCompile(strings.ReplaceAll(pattern, " ", ""))
}

// articleRef matches a cross-reference rather than a heading: "the provisions
// of Article 85 of this Law". The heading form is followed by the article's
// own text, never by "of this Law".
var articleRef = regexp.MustCompile(`(?i)^ofthislaw`)

// ArticleText returns the body of one numbered article.
//
// Articles run in order, so the body of article n is everything between its
// heading and the heading of the next one this document contains. Searching
// forward from the previous article's position rather than from the start is
// what keeps a cross-reference — "as an exception to the provisions of
// Article 85" in Article 87 — from being mistaken for the heading.
//
// `until` is the number of the article that follows. Passing 0 reads to the end
// of the document, which is right only for the last article in a chapter and is
// never what this product wants for Articles 84 and 85.
func ArticleText(doc Scanned, number, until int) (Scanned, error) {
	start, err := articleHeading(doc.Dense, number, 0)
	if err != nil {
		return Scanned{}, err
	}
	end := len(doc.Dense)
	if until > 0 {
		if e, err := articleHeading(doc.Dense, until, start+1); err == nil {
			end = e
		}
	}

	// Past the heading itself, so the evidence quoted from this passage is the
	// article's words rather than its number.
	start += len(fmt.Sprintf("Article%d", number))
	if start > end {
		start = end
	}
	return doc.Slice(start, end), nil
}

// articleHeading finds where article `number` starts in the spaceless text, at
// or after `from`.
func articleHeading(dense string, number, from int) (int, error) {
	needle := fmt.Sprintf("Article%d", number)
	for i := from; ; {
		j := indexFoldAt(dense, needle, i)
		if j < 0 {
			return 0, errs.Newf(errs.CodeInvalidInput,
				"This document does not contain Article %d. It is not the "+
					"law this rule is read out of, or it is an extract that "+
					"stops short of the article.", number)
		}
		after := j + len(needle)
		// "Article 85 of this Law" is somebody citing it, not the heading.
		// So is "Article 851", which is a different article entirely.
		next := ""
		if after < len(dense) {
			next = dense[after:min(after+16, len(dense))]
		}
		if len(next) > 0 && next[0] >= '0' && next[0] <= '9' {
			i = after
			continue
		}
		if articleRef.MatchString(next) {
			i = after
			continue
		}
		return j, nil
	}
}

// indexFoldAt is strings.Index from an offset, ignoring case.
func indexFoldAt(s, needle string, from int) int {
	if from >= len(s) {
		return -1
	}
	i := strings.Index(strings.ToLower(s[from:]), strings.ToLower(needle))
	if i < 0 {
		return -1
	}
	return from + i
}

// SentenceAround returns the sentence the match at `at` sits in.
//
// Evidence is a sentence, not a window of n characters. A reader checking the
// software's reading needs the clause that supports the figure, ending where
// the drafter ended it — a truncated quote is exactly as useless in a
// regulatory trail as no quote.
func SentenceAround(s string, at, length int) string {
	if at < 0 || at > len(s) {
		return ""
	}
	start := 0
	for i := at; i > 0; i-- {
		if s[i-1] == '.' && (i >= len(s) || i == at || s[i] == ' ') {
			start = i
			break
		}
	}
	end := len(s)
	for i := at + length; i < len(s); i++ {
		if s[i] == '.' {
			end = i + 1
			break
		}
	}
	return strings.TrimSpace(s[start:end])
}

// monthDaysPattern reads the statute's own definition of a month.
//
// "half-month wage" is only fifteen days because the same law says a month is
// thirty days. Reading that from the document rather than assuming it is the
// difference between a figure this product can show its working for and a
// number in a table.
var monthDaysPattern = DensePattern(`(?i) month: (\d{1,3}) days `)

// MonthDays returns the number of days the document says a month is.
func MonthDays(doc Scanned) (int, string, error) {
	loc := doc.Find(monthDaysPattern)
	if loc == nil {
		return 0, "", errs.New(errs.CodeInvalidInput,
			"This document does not define how many days a month is, and an "+
				"entitlement stated as a half-month wage cannot be converted "+
				"into days of wage without it.")
	}
	found := doc.At(loc[2], loc[3])
	days, err := strconv.Atoi(found)
	if err != nil || days <= 0 || days > 31 {
		return 0, "", errs.Newf(errs.CodeInvalidInput,
			"This document defines a month as %q days, which cannot be right.",
			found)
	}
	return days, doc.Sentence(loc[0], loc[1]), nil
}
