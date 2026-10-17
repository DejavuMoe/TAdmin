package feeds

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// DefaultMaxBodyBytes is the default limit for a (decompressed) document.
const DefaultMaxBodyBytes int64 = 20 << 20

// htmlHeadLimit bounds how much of an HTML page is scanned for <link> tags.
const htmlHeadLimit = 512 << 10

// maxHTMLAlternates bounds the number of links collected from an HTML page.
const maxHTMLAlternates = 32

// Parse sniffs data by content (the content type is only a hint), decodes
// its character set and parses it as a sitemap, sitemap index, RSS, Atom,
// JSON Feed or HTML page. Gzip-compressed data is decompressed (up to
// DefaultMaxBodyBytes). Relative URLs are resolved against baseURL.
// Content that is none of these yields an error wrapping ErrUnsupported.
func Parse(data []byte, contentType, baseURL string) (*Document, error) {
	return parse(data, contentType, baseURL, DefaultMaxBodyBytes)
}

func parse(data []byte, contentType, baseURL string, limit int64) (*Document, error) {
	data, err := maybeGunzip(data, limit)
	if err != nil {
		return nil, err
	}
	var base *url.URL
	if baseURL != "" {
		if u, err := url.Parse(strings.TrimSpace(baseURL)); err == nil {
			base = u
		}
	}
	mediaType := parseMediaType(contentType)

	body := bytes.TrimLeft(bytes.TrimPrefix(data, utf8BOM), " \t\r\n")
	if len(body) == 0 {
		return nil, fmt.Errorf("%w: empty document", ErrUnsupported)
	}
	switch {
	case body[0] == '{':
		return parseJSONFeed(body, base)
	case body[0] == '<' || utf16Endianness(data) != nil:
		doc, root, err := parseXML(data, contentType, base)
		// Fall back to HTML only when the content is not a recognized
		// sitemap/feed: an unknown root or unparsable markup.
		recognized := rootKind(root) != ""
		if err == nil || recognized || (root == "" && !errors.Is(err, errNotXML)) {
			return doc, err
		}
		if isHTMLish(mediaType, root, body) {
			return parseHTML(data, contentType, base)
		}
		return nil, err
	case isHTMLMediaType(mediaType):
		return parseHTML(data, contentType, base)
	}
	return nil, fmt.Errorf("%w: content does not look like XML, JSON or HTML", ErrUnsupported)
}

var (
	utf8BOM   = []byte{0xEF, 0xBB, 0xBF}
	gzipMagic = []byte{0x1f, 0x8b}
	errNotXML = fmt.Errorf("%w: not well-formed XML", ErrUnsupported)
)

// maybeGunzip transparently decompresses gzip data (up to two nested
// layers), enforcing limit on the decompressed size.
func maybeGunzip(data []byte, limit int64) ([]byte, error) {
	for range 2 {
		if !bytes.HasPrefix(data, gzipMagic) {
			break
		}
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("feeds: invalid gzip data: %w", err)
		}
		out, err := readLimited(zr, limit)
		zr.Close()
		if err != nil {
			if errors.Is(err, ErrTooLarge) {
				return nil, err
			}
			return nil, fmt.Errorf("feeds: invalid gzip data: %w", err)
		}
		data = out
	}
	return data, nil
}

// readLimited reads r fully, failing with ErrTooLarge beyond limit bytes.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: exceeds %d bytes", ErrTooLarge, limit)
	}
	return data, nil
}

func parseMediaType(contentType string) string {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt, _, _ = strings.Cut(contentType, ";")
	}
	return strings.ToLower(strings.TrimSpace(mt))
}

func contentTypeCharset(contentType string) string {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(params["charset"])
}

func isHTMLMediaType(mt string) bool {
	return mt == "text/html" || mt == "application/xhtml+xml"
}

func isHTMLish(mediaType, root string, body []byte) bool {
	if isHTMLMediaType(mediaType) {
		return true
	}
	switch strings.ToLower(root) {
	case "html", "head", "body":
		return true
	}
	head := body[:min(len(body), 64)]
	lower := bytes.ToLower(head)
	return bytes.HasPrefix(lower, []byte("<!doctype html")) || bytes.HasPrefix(lower, []byte("<html"))
}

// ---- XML ----

// xmlNode is a direct child element: name, attributes and own text.
type xmlNode struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Text    string     `xml:",chardata"`
}

// xmlElem is an element with its direct children, decoded namespace-
// agnostically so callers can pick children by local name.
type xmlElem struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Children []xmlNode  `xml:",any"`
}

// child returns the first child with the given local name, preferring one in
// namespace ns (the parent's namespace, i.e. the core vocabulary) over
// extension elements with the same local name.
func (e *xmlElem) child(ns, local string) *xmlNode {
	var fallback *xmlNode
	for i := range e.Children {
		c := &e.Children[i]
		if !strings.EqualFold(c.XMLName.Local, local) {
			continue
		}
		if c.XMLName.Space == ns {
			return c
		}
		if fallback == nil {
			fallback = c
		}
	}
	return fallback
}

func (e *xmlElem) text(ns, local string) string {
	if c := e.child(ns, local); c != nil {
		return strings.TrimSpace(c.Text)
	}
	return ""
}

func attrValue(attrs []xml.Attr, local string) string {
	for _, a := range attrs {
		if strings.EqualFold(a.Name.Local, local) {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

// xmlBaseAttr returns the xml:base attribute value, if any.
func xmlBaseAttr(attrs []xml.Attr) string {
	for _, a := range attrs {
		if a.Name.Local == "base" && (a.Name.Space == "xml" || a.Name.Space == "http://www.w3.org/XML/1998/namespace") {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

func withXMLBase(base *url.URL, attrs []xml.Attr) *url.URL {
	b := xmlBaseAttr(attrs)
	if b == "" {
		return base
	}
	ref, err := url.Parse(b)
	if err != nil {
		return base
	}
	if base != nil {
		return base.ResolveReference(ref)
	}
	return ref
}

// parseXML parses an XML sitemap or feed. root is the local name of the root
// element when one was found; it is empty if data is not XML.
func parseXML(data []byte, contentType string, base *url.URL) (doc *Document, root string, err error) {
	r, charsetReader, err := prepareXML(data, contentType)
	if err != nil {
		return nil, "", err
	}
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = charsetReader

	var start xml.StartElement
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, ErrUnsupported) {
				return nil, "", err
			}
			return nil, "", fmt.Errorf("%w: %v", errNotXML, err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			start = se
			break
		}
	}
	root = start.Name.Local
	kind := rootKind(root)
	if kind == "" {
		return nil, root, fmt.Errorf("%w: unknown root element <%s>", ErrUnsupported, root)
	}
	p := &xmlParser{
		dec:     dec,
		doc:     &Document{Kind: kind},
		base:    withXMLBase(base, start.Attr),
		entries: newEntrySet(MaxEntriesPerFile),
	}
	if err := p.run(strings.ToLower(root)); err != nil {
		return nil, root, fmt.Errorf("feeds: parse %s: %w", kind, err)
	}
	if kind == KindSitemapIndex {
		p.doc.Children = p.entries.items
	} else {
		p.doc.Entries = p.entries.items
	}
	return p.doc, root, nil
}

// rootKind maps an XML root element's local name to a Kind ("" if unknown).
func rootKind(root string) Kind {
	switch strings.ToLower(root) {
	case "urlset":
		return KindSitemap
	case "sitemapindex":
		return KindSitemapIndex
	case "rss", "rdf":
		return KindRSS
	case "feed":
		return KindAtom
	}
	return ""
}

type xmlParser struct {
	dec     *xml.Decoder
	doc     *Document
	base    *url.URL
	entries *entrySet
}

func (p *xmlParser) run(root string) error {
	stack := []string{root}
	for len(stack) > 0 && !p.entries.full() {
		tok, err := p.dec.Token()
		if err == io.EOF || isUnexpectedEOF(err) {
			return nil // tolerate missing end tags at the end of the document
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			handled, err := p.element(t, strings.ToLower(t.Name.Local), stack)
			if err != nil {
				return err
			}
			if !handled {
				stack = append(stack, strings.ToLower(t.Name.Local))
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		}
	}
	return nil
}

func isUnexpectedEOF(err error) bool {
	var se *xml.SyntaxError
	return errors.As(err, &se) && se.Msg == "unexpected EOF"
}

// element handles a start element; it returns true if it consumed the
// element (including its end tag).
func (p *xmlParser) element(t xml.StartElement, name string, stack []string) (bool, error) {
	depth, parent := len(stack), stack[len(stack)-1]
	switch p.doc.Kind {
	case KindSitemap, KindSitemapIndex:
		want := "url"
		if p.doc.Kind == KindSitemapIndex {
			want = "sitemap"
		}
		if depth != 1 || name != want {
			return false, nil
		}
		var e xmlElem
		if err := p.dec.DecodeElement(&e, &t); err != nil {
			return true, err
		}
		ns := t.Name.Space
		p.add(p.base, e.text(ns, "loc"), parseDate(e.text(ns, "lastmod")), "")
		return true, nil

	case KindRSS:
		switch {
		case name == "item":
			var e xmlElem
			if err := p.dec.DecodeElement(&e, &t); err != nil {
				return true, err
			}
			p.rssItem(&e)
			return true, nil
		case name == "title" && parent == "channel" && p.doc.Title == "":
			return true, p.decodeTitle(&t)
		}

	case KindAtom:
		switch {
		case name == "entry":
			var e xmlElem
			if err := p.dec.DecodeElement(&e, &t); err != nil {
				return true, err
			}
			p.atomEntry(&e)
			return true, nil
		case name == "title" && depth == 1 && p.doc.Title == "":
			return true, p.decodeTitle(&t)
		}
	}
	return false, nil
}

func (p *xmlParser) decodeTitle(t *xml.StartElement) error {
	var s string
	if err := p.dec.DecodeElement(&s, t); err != nil {
		return err
	}
	p.doc.Title = cleanText(s)
	return nil
}

func (p *xmlParser) add(base *url.URL, ref string, updated time.Time, title string) {
	if u, ok := resolveRef(base, ref); ok {
		p.entries.add(Entry{URL: u, Updated: updated, Title: cleanText(title)})
	}
}

func (p *xmlParser) rssItem(e *xmlElem) {
	ns := e.XMLName.Space
	base := withXMLBase(p.base, e.Attrs)
	link := rssLink(e, ns)
	updated := firstDate(
		e.text("", "updated"), e.text("", "modified"),
		e.text(ns, "pubDate"), e.text("", "date"), e.text("", "published"),
	)
	p.add(base, link, updated, e.text(ns, "title"))
}

func rssLink(e *xmlElem, ns string) string {
	// Core <link>text</link> first, then any textual link, then an Atom-style
	// <atom:link href> alternate.
	if c := e.child(ns, "link"); c != nil && strings.TrimSpace(c.Text) != "" {
		return c.Text
	}
	for _, c := range e.Children {
		if strings.EqualFold(c.XMLName.Local, "link") && strings.TrimSpace(c.Text) != "" {
			return c.Text
		}
	}
	for _, c := range e.Children {
		if strings.EqualFold(c.XMLName.Local, "link") && isAlternateLink(c.Attrs) {
			return attrValue(c.Attrs, "href")
		}
	}
	for _, c := range e.Children {
		if !strings.EqualFold(c.XMLName.Local, "guid") {
			continue
		}
		v := strings.TrimSpace(c.Text)
		if strings.EqualFold(attrValue(c.Attrs, "isPermaLink"), "true") || looksLikeURL(v) {
			return v
		}
	}
	return attrValue(e.Attrs, "about") // RSS 1.0 rdf:about
}

func isAlternateLink(attrs []xml.Attr) bool {
	if attrValue(attrs, "href") == "" {
		return false
	}
	rel := strings.ToLower(attrValue(attrs, "rel"))
	return rel == "" || rel == "alternate"
}

func (p *xmlParser) atomEntry(e *xmlElem) {
	ns := e.XMLName.Space
	base := withXMLBase(p.base, e.Attrs)
	// Take the first alternate link, preferring an HTML (or untyped) one.
	link, linkBase := "", base
	for _, c := range e.Children {
		if !strings.EqualFold(c.XMLName.Local, "link") || !isAlternateLink(c.Attrs) {
			continue
		}
		t := strings.ToLower(attrValue(c.Attrs, "type"))
		isHTML := t == "" || strings.Contains(t, "html")
		if link == "" || isHTML {
			link, linkBase = attrValue(c.Attrs, "href"), withXMLBase(base, c.Attrs)
		}
		if isHTML {
			break
		}
	}
	base = linkBase
	if link == "" {
		if id := e.text(ns, "id"); looksLikeURL(id) {
			link = id
		}
	}
	updated := firstDate(
		e.text(ns, "updated"), e.text(ns, "modified"),
		e.text(ns, "published"), e.text(ns, "issued"),
	)
	p.add(base, link, updated, e.text(ns, "title"))
}

// prepareXML returns a UTF-8 (or declaration-decoded) reader for data and
// the CharsetReader to install on the decoder.
//
// Precedence: a UTF-16 BOM / byte pattern, then the XML declaration's
// encoding, then the Content-Type charset, then UTF-8 with invalid bytes
// replaced.
func prepareXML(data []byte, contentType string) (io.Reader, func(string, io.Reader) (io.Reader, error), error) {
	data = bytes.TrimPrefix(data, utf8BOM)
	transcoded := false
	if enc := utf16Endianness(data); enc != nil {
		out, _, err := transform.Bytes(unicode.UTF16(*enc, unicode.IgnoreBOM).NewDecoder(), trimUTF16BOM(data))
		if err != nil {
			return nil, nil, fmt.Errorf("%w: invalid UTF-16: %v", ErrUnsupported, err)
		}
		data, transcoded = out, true
	}
	decl := xmlDeclEncoding(data)
	switch {
	case transcoded:
	case decl != "" && !isUTF8Label(decl) && !isUTF16Label(decl):
		// Leave the bytes alone; the decoder calls CharsetReader.
	case decl == "" && contentTypeCharset(contentType) != "" && !isUTF8Label(contentTypeCharset(contentType)):
		cs := contentTypeCharset(contentType)
		r, err := charset.NewReaderLabel(cs, bytes.NewReader(data))
		if err != nil {
			return nil, nil, fmt.Errorf("%w: unsupported character encoding %q", ErrUnsupported, cs)
		}
		out, err := io.ReadAll(r)
		if err != nil {
			return nil, nil, fmt.Errorf("feeds: decode %s: %w", cs, err)
		}
		data, transcoded = out, true
	default:
		data = bytes.ToValidUTF8(data, []byte("�"))
	}
	cr := func(label string, in io.Reader) (io.Reader, error) {
		// Once the whole document was transcoded to UTF-8 the declaration
		// is stale; a UTF-16 declaration on ASCII-compatible bytes is a lie.
		if transcoded || isUTF16Label(label) || isUTF8Label(label) {
			return in, nil
		}
		r, err := charset.NewReaderLabel(label, in)
		if err != nil {
			return nil, fmt.Errorf("%w: unsupported character encoding %q", ErrUnsupported, label)
		}
		return r, nil
	}
	return bytes.NewReader(data), cr, nil
}

func isUTF8Label(l string) bool {
	l = strings.ToLower(strings.TrimSpace(l))
	return l == "utf-8" || l == "utf8" || l == "unicode-1-1-utf-8"
}

func isUTF16Label(l string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(l)), "utf-16")
}

// utf16Endianness detects UTF-16 by BOM or by the "<?" byte pattern.
func utf16Endianness(data []byte) *unicode.Endianness {
	le, be := unicode.LittleEndian, unicode.BigEndian
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}), bytes.HasPrefix(data, []byte{'<', 0, '?', 0}):
		return &le
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}), bytes.HasPrefix(data, []byte{0, '<', 0, '?'}):
		return &be
	}
	return nil
}

func trimUTF16BOM(data []byte) []byte {
	if bytes.HasPrefix(data, []byte{0xFF, 0xFE}) || bytes.HasPrefix(data, []byte{0xFE, 0xFF}) {
		return data[2:]
	}
	return data
}

// xmlDeclEncoding extracts encoding="..." from a leading XML declaration.
func xmlDeclEncoding(data []byte) string {
	head := bytes.TrimLeft(data[:min(len(data), 1024)], " \t\r\n")
	if !bytes.HasPrefix(head, []byte("<?xml")) {
		return ""
	}
	end := bytes.Index(head, []byte("?>"))
	if end < 0 {
		return ""
	}
	decl := string(head[:end])
	i := strings.Index(decl, "encoding")
	if i < 0 {
		return ""
	}
	rest := strings.TrimLeft(decl[i+len("encoding"):], " \t\r\n")
	if !strings.HasPrefix(rest, "=") {
		return ""
	}
	rest = strings.TrimLeft(rest[1:], " \t\r\n")
	if rest == "" || (rest[0] != '"' && rest[0] != '\'') {
		return ""
	}
	q := rest[0]
	rest = rest[1:]
	j := strings.IndexByte(rest, q)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

// ---- JSON Feed ----

type jsonFeed struct {
	Version string    `json:"version"`
	Title   string    `json:"title"`
	Items   jsonItems `json:"items"`
}

type jsonItems []jsonItem

func (items *jsonItems) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('[') {
		return fmt.Errorf("items must be an array")
	}
	for d.More() {
		if len(*items) >= MaxEntriesPerFile {
			return ErrTooLarge
		}
		var item jsonItem
		if err := d.Decode(&item); err != nil {
			return err
		}
		*items = append(*items, item)
	}
	_, err = d.Token()
	return err
}

type jsonItem struct {
	ID            json.RawMessage `json:"id"`
	URL           string          `json:"url"`
	ExternalURL   string          `json:"external_url"`
	Title         string          `json:"title"`
	DateModified  string          `json:"date_modified"`
	DatePublished string          `json:"date_published"`
}

func parseJSONFeed(data []byte, base *url.URL) (*Document, error) {
	var f jsonFeed
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON: %w", ErrUnsupported, err)
	}
	if !strings.Contains(strings.ToLower(f.Version), "jsonfeed.org/version/") {
		return nil, fmt.Errorf("%w: JSON document is not a JSON Feed", ErrUnsupported)
	}
	doc := &Document{Kind: KindJSONFeed, Title: cleanText(f.Title)}
	set := newEntrySet(MaxEntriesPerFile)
	for _, it := range f.Items {
		if set.full() {
			break
		}
		ref := it.URL
		if strings.TrimSpace(ref) == "" {
			ref = it.ExternalURL
		}
		if strings.TrimSpace(ref) == "" {
			var id string
			if json.Unmarshal(it.ID, &id) == nil && looksLikeURL(id) {
				ref = id
			}
		}
		u, ok := resolveRef(base, ref)
		if !ok {
			continue
		}
		set.add(Entry{
			URL:     u,
			Updated: firstDate(it.DateModified, it.DatePublished),
			Title:   cleanText(it.Title),
		})
	}
	doc.Entries = set.items
	return doc, nil
}

// ---- HTML ----

type htmlLink struct{ rel, typ, href, title string }

func parseHTML(data []byte, contentType string, base *url.URL) (*Document, error) {
	var r io.Reader = bytes.NewReader(data)
	if cr, err := charset.NewReader(r, contentType); err == nil {
		r = cr
	}
	z := html.NewTokenizer(io.LimitReader(r, htmlHeadLimit))
	doc := &Document{Kind: KindHTML}
	var links []htmlLink
	baseHref := ""
scan:
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			break scan
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch string(name) {
			case "body":
				break scan
			case "title":
				if tt == html.StartTagToken && doc.Title == "" && z.Next() == html.TextToken {
					doc.Title = cleanText(string(z.Text()))
				}
			case "base":
				if a := tagAttrs(z, hasAttr); baseHref == "" {
					baseHref = a["href"]
				}
			case "link":
				if len(links) < maxHTMLAlternates {
					a := tagAttrs(z, hasAttr)
					links = append(links, htmlLink{rel: a["rel"], typ: a["type"], href: a["href"], title: a["title"]})
				}
			}
		}
	}
	if baseHref != "" {
		if ref, err := url.Parse(strings.TrimSpace(baseHref)); err == nil {
			if base != nil {
				base = base.ResolveReference(ref)
			} else {
				base = ref
			}
		}
	}
	seen := make(map[string]bool)
	for _, l := range links {
		kind := linkKind(l.rel, l.typ)
		if kind == "" {
			continue
		}
		u, ok := resolveRef(base, l.href)
		if !ok || seen[u] {
			continue
		}
		seen[u] = true
		doc.Alternates = append(doc.Alternates, Candidate{URL: u, Kind: kind, Title: cleanText(l.title)})
	}
	return doc, nil
}

func tagAttrs(z *html.Tokenizer, more bool) map[string]string {
	attrs := make(map[string]string)
	for more {
		var k, v []byte
		k, v, more = z.TagAttr()
		if _, dup := attrs[string(k)]; !dup {
			attrs[string(k)] = string(v)
		}
	}
	return attrs
}

// linkKind classifies an HTML <link rel type> pair; "" means not a source.
func linkKind(rel, typ string) Kind {
	rels := strings.Fields(strings.ToLower(rel))
	has := func(want string) bool {
		for _, r := range rels {
			if r == want {
				return true
			}
		}
		return false
	}
	if has("sitemap") {
		return KindSitemap
	}
	if !has("alternate") {
		return ""
	}
	switch parseMediaType(typ) {
	case "application/rss+xml", "application/rdf+xml", "text/xml", "application/xml":
		return KindRSS
	case "application/atom+xml":
		return KindAtom
	case "application/feed+json", "application/json":
		return KindJSONFeed
	}
	return ""
}
