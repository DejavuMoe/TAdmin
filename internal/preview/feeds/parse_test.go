package feeds

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v.UTC()
}

type wantEntry struct {
	url     string
	updated string // RFC3339 or ""
	title   string
}

func checkEntries(t *testing.T, got []Entry, want []wantEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.URL != w.url {
			t.Errorf("entry %d URL = %q, want %q", i, g.URL, w.url)
		}
		var wt time.Time
		if w.updated != "" {
			wt = mustTime(t, w.updated)
		}
		if !g.Updated.Equal(wt) {
			t.Errorf("entry %d Updated = %v, want %v", i, g.Updated, wt)
		}
		if w.title != "" && g.Title != w.title {
			t.Errorf("entry %d Title = %q, want %q", i, g.Title, w.title)
		}
	}
}

const sitemapXML = `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"
        xmlns:image="http://www.google.com/schemas/sitemap-image/1.1"
        xmlns:video="http://www.google.com/schemas/sitemap-video/1.1">
  <url>
    <loc>
      https://Example.COM:443/a?x=1&amp;y=2#frag
    </loc>
    <lastmod>2024-05-06</lastmod>
    <image:image><image:loc>https://example.com/img.png</image:loc></image:image>
  </url>
  <url><loc>/relative/b</loc><lastmod>2024-05-06T07:08Z</lastmod></url>
  <url><loc>c</loc><lastmod>2024-05-06T07:08:09+02:00</lastmod></url>
  <url><loc>https://example.com/d</loc><lastmod>2024-05-06T07:08:09.123Z</lastmod>
    <video:video><video:content_loc>https://example.com/v.mp4</video:content_loc></video:video></url>
  <url><loc>https://example.com/a?x=1&amp;y=2</loc></url>
  <url><loc>ftp://example.com/nope</loc></url>
  <url><loc>mailto:someone@example.com</loc></url>
  <url><loc>http://example.com:80/e</loc><lastmod>garbage</lastmod></url>
</urlset>`

func TestParseSitemap(t *testing.T) {
	doc, err := Parse([]byte(sitemapXML), "application/xml", "https://example.com/dir/sitemap.xml")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != KindSitemap {
		t.Fatalf("Kind = %q", doc.Kind)
	}
	checkEntries(t, doc.Entries, []wantEntry{
		{url: "https://example.com/a?x=1&y=2", updated: "2024-05-06T00:00:00Z"},
		{url: "https://example.com/relative/b", updated: "2024-05-06T07:08:00Z"},
		{url: "https://example.com/dir/c", updated: "2024-05-06T05:08:09Z"},
		{url: "https://example.com/d", updated: "2024-05-06T07:08:09.123Z"},
		{url: "http://example.com/e"},
	})
}

func TestParseSitemapNoNamespaceAndOddCase(t *testing.T) {
	// No namespace, prefixed root, and missing root end tag.
	data := `<sm:urlset xmlns:sm="http://www.sitemaps.org/schemas/sitemap/0.9"><sm:url><sm:loc>https://example.com/x</sm:loc></sm:url>`
	doc, err := Parse([]byte(data), "", "")
	if err != nil {
		t.Fatal(err)
	}
	checkEntries(t, doc.Entries, []wantEntry{{url: "https://example.com/x"}})
}

func TestParseSitemapIndex(t *testing.T) {
	data := `<?xml version="1.0"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>https://example.com/s1.xml</loc><lastmod>2023-01-02T03:04:05+00:00</lastmod></sitemap>
  <sitemap><loc>/s2.xml.gz</loc></sitemap>
  <sitemap><loc>https://example.com/s1.xml</loc></sitemap>
</sitemapindex>`
	doc, err := Parse([]byte(data), "text/xml", "https://example.com/sitemap_index.xml")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != KindSitemapIndex || len(doc.Entries) != 0 {
		t.Fatalf("Kind = %q, entries = %d", doc.Kind, len(doc.Entries))
	}
	checkEntries(t, doc.Children, []wantEntry{
		{url: "https://example.com/s1.xml", updated: "2023-01-02T03:04:05Z"},
		{url: "https://example.com/s2.xml.gz"},
	})
}

func TestParseEntryCap(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for i := range MaxEntriesPerFile + 10 {
		fmt.Fprintf(&b, "<url><loc>https://example.com/p/%d</loc></url>", i)
	}
	b.WriteString(`</urlset>`)
	doc, err := Parse([]byte(b.String()), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Entries) != MaxEntriesPerFile {
		t.Fatalf("entries = %d, want %d", len(doc.Entries), MaxEntriesPerFile)
	}
}

const rss2XML = `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:atom="http://www.w3.org/2005/Atom"
     xmlns:media="http://search.yahoo.com/mrss/">
<channel>
  <title>  My   Blog &amp; Co </title>
  <link>https://blog.example.com/</link>
  <atom:link href="https://blog.example.com/feed" rel="self" type="application/rss+xml"/>
  <image><title>Logo</title><url>https://blog.example.com/logo.png</url></image>
  <item>
    <title>First</title>
    <media:title>Media title should not win</media:title>
    <link>https://blog.example.com/first/</link>
    <pubDate>Mon, 02 Jan 2006 15:04:05 -0700</pubDate>
  </item>
  <item>
    <title>Second&nbsp;post <![CDATA[<b>x</b>]]></title>
    <link> /second </link>
    <pubDate>Tue, 3 Jan 2006 10:00:00 GMT</pubDate>
  </item>
  <item>
    <title>GUID permalink</title>
    <guid isPermaLink="true">https://blog.example.com/third</guid>
    <pubDate>Wed, 04 Jan 06 08:00 EST</pubDate>
  </item>
  <item>
    <title>GUID looks like URL</title>
    <guid>https://blog.example.com/fourth</guid>
    <dc:date>2006-01-05T00:00:00Z</dc:date>
  </item>
  <item>
    <title>GUID not a URL</title>
    <guid isPermaLink="false">tag:blog.example.com,2006:5</guid>
  </item>
  <item>
    <title>Atom link only</title>
    <atom:link rel="alternate" href="https://blog.example.com/sixth"/>
    <pubDate>Thursday, 05-Jan-06 12:00:00 PST</pubDate>
  </item>
  <item><title>dup</title><link>https://blog.example.com/first/#comments</link></item>
  <item><title>sloppy</title><link>https://blog.example.com/seventh</link><pubDate>Fri,6 Jan 2006 1:02:03 +0000</pubDate></item>
</channel>
</rss>`

func TestParseRSS2(t *testing.T) {
	doc, err := Parse([]byte(rss2XML), "application/rss+xml", "https://blog.example.com/feed")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != KindRSS {
		t.Fatalf("Kind = %q", doc.Kind)
	}
	if doc.Title != "My Blog & Co" {
		t.Errorf("Title = %q", doc.Title)
	}
	checkEntries(t, doc.Entries, []wantEntry{
		{url: "https://blog.example.com/first/", updated: "2006-01-02T22:04:05Z", title: "First"},
		{url: "https://blog.example.com/second", updated: "2006-01-03T10:00:00Z", title: "Second post <b>x</b>"},
		{url: "https://blog.example.com/third", updated: "2006-01-04T13:00:00Z"},
		{url: "https://blog.example.com/fourth", updated: "2006-01-05T00:00:00Z"},
		{url: "https://blog.example.com/sixth", updated: "2006-01-05T20:00:00Z"},
		{url: "https://blog.example.com/seventh", updated: "2006-01-06T01:02:03Z"},
	})
}

func TestParseRDF(t *testing.T) {
	data := `<?xml version="1.0"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns="http://purl.org/rss/1.0/"
         xmlns:dc="http://purl.org/dc/elements/1.1/">
  <channel rdf:about="https://example.org/">
    <title>RDF Site</title>
    <link>https://example.org/</link>
    <items><rdf:Seq><rdf:li rdf:resource="https://example.org/1"/></rdf:Seq></items>
  </channel>
  <item rdf:about="https://example.org/1">
    <title>One</title><link>https://example.org/1</link><dc:date>2020-02-03T04:05:06+01:00</dc:date>
  </item>
  <item rdf:about="https://example.org/2"><title>Two (about only)</title></item>
</rdf:RDF>`
	doc, err := Parse([]byte(data), "application/rdf+xml", "https://example.org/index.rdf")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != KindRSS || doc.Title != "RDF Site" {
		t.Fatalf("Kind = %q Title = %q", doc.Kind, doc.Title)
	}
	checkEntries(t, doc.Entries, []wantEntry{
		{url: "https://example.org/1", updated: "2020-02-03T03:05:06Z", title: "One"},
		{url: "https://example.org/2"},
	})
}

func TestParseAtom(t *testing.T) {
	data := `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xml:base="https://atom.example.com/blog/">
  <title type="text">Atom Feed</title>
  <link rel="self" href="https://atom.example.com/atom.xml"/>
  <entry>
    <title>Alt</title>
    <link rel="self" href="https://atom.example.com/api/1"/>
    <link rel="enclosure" href="https://atom.example.com/1.mp3"/>
    <link rel="alternate" type="application/json" href="https://atom.example.com/1.json"/>
    <link rel="alternate" type="text/html" href="posts/1"/>
    <updated>2021-03-04T05:06:07Z</updated>
    <published>2021-01-01T00:00:00Z</published>
  </entry>
  <entry xml:base="/other/">
    <title>No rel</title>
    <link href="two"/>
    <published>2021-02-02T02:02:02-05:00</published>
  </entry>
  <entry>
    <title>ID only</title>
    <id>https://atom.example.com/three</id>
  </entry>
  <entry><title>Nothing</title><id>urn:uuid:1234</id></entry>
</feed>`
	doc, err := Parse([]byte(data), "application/atom+xml", "https://atom.example.com/atom.xml")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != KindAtom || doc.Title != "Atom Feed" {
		t.Fatalf("Kind = %q Title = %q", doc.Kind, doc.Title)
	}
	checkEntries(t, doc.Entries, []wantEntry{
		{url: "https://atom.example.com/blog/posts/1", updated: "2021-03-04T05:06:07Z", title: "Alt"},
		{url: "https://atom.example.com/other/two", updated: "2021-02-02T07:02:02Z", title: "No rel"},
		{url: "https://atom.example.com/three"},
	})
}

func TestParseJSONFeed(t *testing.T) {
	data := `{
  "version": "https://jsonfeed.org/version/1.1",
  "title": "JSON Feed",
  "items": [
    {"id": "1", "url": "https://j.example.com/1", "title": "One", "date_modified": "2022-01-02T03:04:05Z", "date_published": "2021-01-01T00:00:00Z"},
    {"id": "2", "external_url": "/2", "date_published": "2022-02-02T00:00:00+08:00"},
    {"id": "https://j.example.com/3"},
    {"id": 4, "content_text": "no url"},
    {"id": "5", "url": "https://j.example.com/1#dup"}
  ]
}`
	doc, err := Parse([]byte(data), "application/feed+json", "https://j.example.com/feed.json")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Kind != KindJSONFeed || doc.Title != "JSON Feed" {
		t.Fatalf("Kind = %q Title = %q", doc.Kind, doc.Title)
	}
	checkEntries(t, doc.Entries, []wantEntry{
		{url: "https://j.example.com/1", updated: "2022-01-02T03:04:05Z", title: "One"},
		{url: "https://j.example.com/2", updated: "2022-02-01T16:00:00Z"},
		{url: "https://j.example.com/3"},
	})
}

func TestParseCharsets(t *testing.T) {
	latin1Title := []byte("Caf\xe9 \xfcber")
	tests := []struct {
		name, ct string
		data     []byte
	}{
		{
			name: "declaration ISO-8859-1",
			data: fmt.Appendf(nil, `<?xml version="1.0" encoding="ISO-8859-1"?><rss><channel><title>%s</title><item><title>%s</title><link>https://e.com/1</link></item></channel></rss>`, latin1Title, latin1Title),
		},
		{
			name: "content-type charset only",
			ct:   "text/xml; charset=iso-8859-1",
			data: fmt.Appendf(nil, `<rss><channel><title>%s</title><item><title>%s</title><link>https://e.com/1</link></item></channel></rss>`, latin1Title, latin1Title),
		},
		{
			name: "windows-1252 declaration",
			data: fmt.Appendf(nil, `<?xml version='1.0' encoding='windows-1252'?><rss><channel><title>%s</title><item><title>%s</title><link>https://e.com/1</link></item></channel></rss>`, latin1Title, latin1Title),
		},
		{
			name: "utf-8 with BOM",
			data: append([]byte("\xef\xbb\xbf"), `<rss><channel><title>Café über</title><item><title>Café über</title><link>https://e.com/1</link></item></channel></rss>`...),
		},
		{
			name: "utf-16 declaration on utf-8 bytes",
			data: []byte(`<?xml version="1.0" encoding="UTF-16"?><rss><channel><title>Café über</title><item><title>Café über</title><link>https://e.com/1</link></item></channel></rss>`),
		},
		{
			name: "utf-16le with BOM",
			data: utf16LE(string(rune(0xFEFF)) + `<?xml version="1.0" encoding="UTF-16"?><rss><channel><title>Café über</title><item><title>Café über</title><link>https://e.com/1</link></item></channel></rss>`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := Parse(tt.data, tt.ct, "")
			if err != nil {
				t.Fatal(err)
			}
			if doc.Title != "Café über" {
				t.Errorf("Title = %q", doc.Title)
			}
			checkEntries(t, doc.Entries, []wantEntry{{url: "https://e.com/1", title: "Café über"}})
		})
	}

	t.Run("unknown encoding", func(t *testing.T) {
		_, err := Parse([]byte(`<?xml version="1.0" encoding="x-klingon"?><rss/>`), "", "")
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("err = %v, want ErrUnsupported", err)
		}
	})
	t.Run("invalid utf-8 is replaced", func(t *testing.T) {
		doc, err := Parse([]byte("<rss><channel><title>bad\xff</title></channel></rss>"), "", "")
		if err != nil {
			t.Fatal(err)
		}
		if doc.Title != "bad�" {
			t.Errorf("Title = %q", doc.Title)
		}
	})
}

func utf16LE(s string) []byte {
	var b []byte
	for _, r := range s {
		if r > 0xFFFF {
			panic("BMP only")
		}
		b = append(b, byte(r), byte(r>>8))
	}
	return b
}

func TestParseHTML(t *testing.T) {
	page := `<!DOCTYPE html>
<html lang=en><head>
<meta charset="utf-8">
<title> Example
 Site </title>
<base href="https://cdn.example.com/base/">
<link rel="stylesheet" href="/style.css">
<link rel="alternate" type="application/rss+xml" title="RSS" href="/feed.xml">
<link rel="alternate" type="application/atom+xml; charset=utf-8" title="Atom" href="atom.xml">
<link rel="alternate" type="application/feed+json" href="https://example.com/feed.json">
<link rel="alternate" type="application/json" href="https://example.com/feed.json">
<link rel="alternate" hreflang="de" href="https://example.com/de/">
<link rel="Sitemap" type="application/xml" href="/sitemap.xml">
<link rel="alternate" type="application/rss+xml" href="javascript:alert(1)">
</head>
<body>
<link rel="alternate" type="application/rss+xml" href="/after-body.xml">
</body></html>`
	for _, ct := range []string{"text/html; charset=utf-8", ""} {
		doc, err := Parse([]byte(page), ct, "https://example.com/page")
		if err != nil {
			t.Fatal(err)
		}
		if doc.Kind != KindHTML || doc.Title != "Example Site" {
			t.Fatalf("Kind = %q Title = %q", doc.Kind, doc.Title)
		}
		want := []Candidate{
			{URL: "https://cdn.example.com/feed.xml", Kind: KindRSS, Title: "RSS"},
			{URL: "https://cdn.example.com/base/atom.xml", Kind: KindAtom, Title: "Atom"},
			{URL: "https://example.com/feed.json", Kind: KindJSONFeed},
			{URL: "https://cdn.example.com/sitemap.xml", Kind: KindSitemap},
		}
		if fmt.Sprint(doc.Alternates) != fmt.Sprint(want) {
			t.Errorf("Alternates =\n%v\nwant\n%v", doc.Alternates, want)
		}
	}
}

func TestParseHTMLLatin1Title(t *testing.T) {
	page := []byte("<html><head><meta http-equiv=\"Content-Type\" content=\"text/html; charset=iso-8859-1\"><title>Caf\xe9</title></head></html>")
	doc, err := Parse(page, "", "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "Café" {
		t.Errorf("Title = %q", doc.Title)
	}
}

func TestParseUnsupported(t *testing.T) {
	tests := map[string]struct{ data, ct string }{
		"empty":         {"  \n", ""},
		"plain text":    {"User-agent: *\nDisallow: /", "text/plain"},
		"json not feed": {`{"hello":"world"}`, "application/json"},
		"bad json":      {`{"version":`, "application/json"},
		"unknown root":  {`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`, "image/svg+xml"},
		"not xml":       {`<<<>>>`, "application/xml"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tt.data), tt.ct, "")
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("err = %v, want ErrUnsupported", err)
			}
		})
	}
}

func TestParseGzipMagic(t *testing.T) {
	gz := gzipBytes(t, []byte(`<urlset><url><loc>https://example.com/gz</loc></url></urlset>`))
	doc, err := Parse(gz, "application/x-gzip", "")
	if err != nil {
		t.Fatal(err)
	}
	checkEntries(t, doc.Entries, []wantEntry{{url: "https://example.com/gz"}})

	if _, err := parse(gzipBytes(t, bytes.Repeat([]byte("a"), 2048)), "", "", 1024); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func gzipBytes(t testing.TB, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestParseDate(t *testing.T) {
	tests := []struct{ in, want string }{
		{"2024", "2024-01-01T00:00:00Z"},
		{"2024-05", "2024-05-01T00:00:00Z"},
		{"2024-05-06", "2024-05-06T00:00:00Z"},
		{"2024-05-06T07:08Z", "2024-05-06T07:08:00Z"},
		{"2024-05-06T07:08+01:00", "2024-05-06T06:08:00Z"},
		{"2024-05-06T07:08:09Z", "2024-05-06T07:08:09Z"},
		{"2024-05-06t07:08:09z", "2024-05-06T07:08:09Z"},
		{"2024-05-06T07:08:09.5-03:00", "2024-05-06T10:08:09.5Z"},
		{"2024-05-06T07:08:09+0200", "2024-05-06T05:08:09Z"},
		{"2024-05-06T07:08:09", "2024-05-06T07:08:09Z"},
		{"2024-05-06 07:08:09", "2024-05-06T07:08:09Z"},
		{"2024-05-06 07:08:09 +0000", "2024-05-06T07:08:09Z"},
		{"Mon, 02 Jan 2006 15:04:05 -0700", "2006-01-02T22:04:05Z"},
		{"Mon, 02 Jan 2006 15:04:05 GMT", "2006-01-02T15:04:05Z"},
		{"Mon, 2 Jan 2006 15:04:05 UT", "2006-01-02T15:04:05Z"},
		{"Mon, 02 Jan 06 15:04 EDT", "2006-01-02T19:04:00Z"},
		{"Monday, 02-Jan-06 15:04:05 PST", "2006-01-02T23:04:05Z"},
		{"Mon,02 Jan 2006 15:04:05 +00:00", "2006-01-02T15:04:05Z"},
		{"Sun, 02 Jan 2006 15:04:05 +0000", "2006-01-02T15:04:05Z"}, // wrong weekday
		{"02 January 2006 15:04:05 +0100", "2006-01-02T14:04:05Z"},
		{"Mon, 02 Jan 2006 15:04:05 +0000 (UTC)", "2006-01-02T15:04:05Z"},
		{"Mon, 02 Jan 2006", "2006-01-02T00:00:00Z"},
		{"Jan 2, 2006 3:04:05 GMT", "2006-01-02T03:04:05Z"},
		{"Jan 2, 2006", "2006-01-02T00:00:00Z"},
		{"Mon Jan  2 15:04:05 2006", "2006-01-02T15:04:05Z"},
		{"Mon Jan 2 15:04:05 MST 2006", "2006-01-02T22:04:05Z"},
		{"", ""},
		{"yesterday", ""},
	}
	for _, tt := range tests {
		got := parseDate(tt.in)
		var want time.Time
		if tt.want != "" {
			want = mustTime(t, tt.want)
		}
		if !got.Equal(want) {
			t.Errorf("parseDate(%q) = %v, want %v", tt.in, got, want)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"HTTPS://Example.COM:443/Path?Q=A#frag", "https://example.com/Path?Q=A"},
		{"http://example.com:80", "http://example.com/"},
		{"http://example.com:8080/x", "http://example.com:8080/x"},
		{"https://example.com:80/x", "https://example.com:80/x"},
		{"  https://[2001:DB8::1]:443/a  ", "https://[2001:db8::1]/a"},
		{"https://example.com/a%2Fb?x=%20", "https://example.com/a%2Fb?x=%20"},
		{"ftp://example.com/", ""},
		{"mailto:a@b.c", ""},
		{"/relative", ""},
		{"https://", ""},
		{"http://exa mple.com/", ""},
	}
	for _, tt := range tests {
		got, err := NormalizeURL(tt.in)
		if tt.want == "" {
			if !errors.Is(err, ErrInvalidURL) {
				t.Errorf("NormalizeURL(%q) = %q, %v; want ErrInvalidURL", tt.in, got, err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("NormalizeURL(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}
