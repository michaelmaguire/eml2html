package eml2html

import (
	"bytes"
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// crlf turns a readable message into one with RFC 822 line endings
func crlf(s string) string {
	return strings.ReplaceAll(strings.TrimLeft(s, "\n"), "\n", "\r\n")
}

// a 1x1 PNG
var pngBytes, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")

var pngB64 = base64.StdEncoding.EncodeToString(pngBytes)

var textMsg = crlf(textMsgRaw)

var textMsgRaw = `
From: Alice <alice@example.com>
To: Bob <bob@example.com>
Subject: Plain <b>hello</b>
Date: Thu, 13 Aug 2026 08:00:00 +0200
Content-Type: text/plain; charset=utf-8

Hi Bob,
1 < 2 & "quoted"
`

var htmlMsg = crlf(`
From: Shop <shop@example.com>
To: Bob <bob@example.com>
Subject: Your order
Date: Thu, 13 Aug 2026 08:00:00 +0200
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="mixed"

--mixed
Content-Type: multipart/related; boundary="related"

--related
Content-Type: multipart/alternative; boundary="alt"

--alt
Content-Type: text/plain; charset=utf-8

Plain alternative.
--alt
Content-Type: text/html; charset=utf-8

<html><head><title>x</title></head><body>
<p onclick="steal()">Thanks for your order</p>
<script>alert("xss")</script>
<img src="cid:logo@shop">
<img src="https://tracker.example.com/open.gif">
<a href="https://shop.example.com/order">Order</a>
</body></html>
--alt--
--related
Content-Type: image/png
Content-ID: <logo@shop>
Content-Disposition: inline; filename="logo.png"
Content-Transfer-Encoding: base64

` + pngB64 + `
--related--
--mixed
Content-Type: application/pdf; name="invoice.pdf"
Content-Disposition: attachment; filename="invoice.pdf"
Content-Transfer-Encoding: base64

JVBERi0xLjQK
--mixed
Content-Type: image/png; name="photo.png"
Content-Disposition: attachment; filename="photo.png"
Content-Transfer-Encoding: base64

` + pngB64 + `
--mixed--
`)

var forwardMsg = crlf(`
From: Bob <bob@example.com>
To: Carol <carol@example.com>
Subject: Fwd: plain
Date: Fri, 14 Aug 2026 09:00:00 +0200
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="fwd"

--fwd
Content-Type: text/plain; charset=utf-8

See below.
--fwd
Content-Type: message/rfc822
Content-Disposition: attachment; filename="forwarded.eml"

` + strings.TrimLeft(textMsgRaw, "\n") + `
--fwd--
`)

func render(t *testing.T, msg string, opts *SingleOptions) string {
	t.Helper()
	buf := new(bytes.Buffer)
	if err := WriteSingle(buf, strings.NewReader(msg), opts); err != nil {
		t.Fatalf("WriteSingle: %v", err)
	}
	return buf.String()
}

func mustContain(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output doesn't contain %q", w)
		}
	}
}

func mustNotContain(t *testing.T, out string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(out, u) {
			t.Errorf("output contains %q", u)
		}
	}
}

func TestSingleText(t *testing.T) {
	out := render(t, textMsg, nil)
	mustContain(t, out,
		"<!doctype html>",
		"Plain &lt;b&gt;hello&lt;/b&gt;",
		"Alice &lt;alice@example.com&gt;",
		`<pre class="text">`,
		"1 &lt; 2 &amp; &#34;quoted&#34;",
	)
	mustNotContain(t, out, "<iframe", "<b>hello</b>")
}

func TestSingleHTMLIsSandboxed(t *testing.T) {
	out := render(t, htmlMsg, nil)

	//the HTML body is only inside the sandboxed iframe's srcdoc, escaped
	mustContain(t, out, `<iframe class="html" sandbox="allow-popups allow-popups-to-escape-sandbox" srcdoc="`)
	mustNotContain(t, out, "allow-scripts", "allow-same-origin", "allow-forms", "<script", "<p onclick")
	mustContain(t, out, "&lt;script&gt;")

	//remote content is blocked, links open in a new tab
	mustContain(t, out, "Content-Security-Policy", "img-src data:", "&lt;base target=&#34;_blank&#34;&gt;")

	//the cid: image is embedded, and not listed again as an attachment
	mustContain(t, out, "data:image/png;base64,"+pngB64)
	mustNotContain(t, out, "cid:logo@shop", ">logo.png")

	//the plain-text alternative isn't shown when there's HTML
	mustNotContain(t, out, "Plain alternative.")
}

func TestSingleSameOriginFrames(t *testing.T) {
	out := render(t, htmlMsg, &SingleOptions{SameOriginFrames: true})
	mustContain(t, out, `<iframe class="html" sandbox="allow-same-origin allow-popups allow-popups-to-escape-sandbox" srcdoc="`)
	mustNotContain(t, out, "allow-scripts", "<script")
	mustContain(t, out, "Content-Security-Policy") //remote content is still blocked
}

func TestSingleRemoteContentAllowed(t *testing.T) {
	out := render(t, htmlMsg, &SingleOptions{AllowRemoteContent: true})
	mustNotContain(t, out, "Content-Security-Policy")
	mustContain(t, out, "tracker.example.com/open.gif")
}

func TestSingleAttachments(t *testing.T) {
	out := render(t, htmlMsg, nil)
	mustContain(t, out,
		"invoice.pdf <span class=\"meta\">(application/pdf, 9 B)</span>",
		"photo.png <span class=\"meta\">(image/png,",
		`<img src="data:image/png;base64,`+pngB64+`" alt="photo.png">`,
	)

	out = render(t, htmlMsg, &SingleOptions{MaxInlineImageSize: -1})
	mustNotContain(t, out, "data:image/png")
	mustContain(t, out, "photo.png", "cid:logo@shop")
}

func TestSingleTooBigInlineImageIsListed(t *testing.T) {
	//the cid: image is over the limit: not embedded, but it mustn't disappear either
	out := render(t, htmlMsg, &SingleOptions{MaxInlineImageSize: len(pngBytes) - 1})
	mustNotContain(t, out, "data:image/png")
	mustContain(t, out, "cid:logo@shop", "logo.png <span class=\"meta\">(image/png,")
}

func TestSingleAttachedMessage(t *testing.T) {
	out := render(t, forwardMsg, nil)
	mustContain(t, out, "See below.", "forwarded.eml", "<details><summary>Show attached message</summary>",
		"Plain &lt;b&gt;hello&lt;/b&gt;", "1 &lt; 2")

	out = render(t, forwardMsg, &SingleOptions{MaxDepth: 1})
	mustContain(t, out, "forwarded.eml")
	mustNotContain(t, out, "<details>", "1 &lt; 2")
}

func TestSingleBadInput(t *testing.T) {
	buf := new(bytes.Buffer)
	if err := WriteSingle(buf, strings.NewReader(""), nil); err == nil && !strings.Contains(buf.String(), "No content found") {
		t.Errorf("empty input: no error and no 'No content found'")
	}
}

// the existing directory output is unchanged: files, layout and links
type memFile struct {
	*bytes.Reader
	name string
}

func (f *memFile) Stat() (fs.FileInfo, error) { return memInfo{f.name, int64(f.Len())}, nil }
func (f *memFile) Close() error               { return nil }

type memInfo struct {
	name string
	size int64
}

func (i memInfo) Name() string       { return i.name }
func (i memInfo) Size() int64        { return i.size }
func (i memInfo) Mode() fs.FileMode  { return 0644 }
func (i memInfo) ModTime() time.Time { return time.Time{} }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() interface{}   { return nil }

func TestWriteDirUnchanged(t *testing.T) {
	root := t.TempDir()
	files := []fs.File{&memFile{bytes.NewReader([]byte(htmlMsg)), "order.eml"}}
	if err := WriteDir(root, "mail", files, nil); err != nil {
		t.Fatalf("WriteDir: %v", err)
	}
	for _, p := range []string{
		"mail/index.html",
		"mail/order.eml/order.eml",
		"mail/order.eml/index.html",
		"mail/order.eml/content/order.html",
		"mail/order.eml/attachments/invoice.pdf",
		"mail/order.eml/attachments/logo.png",
	} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	body, err := os.ReadFile(filepath.Join(root, "mail/order.eml/content/order.html"))
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, string(body), `src="../attachments/logo.png"`)
	index, err := os.ReadFile(filepath.Join(root, "mail/order.eml/index.html"))
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, string(index), `<iframe src="content/order.html"></iframe>`, `href="attachments/invoice.pdf"`)
}
