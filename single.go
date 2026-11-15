package eml2html

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html"
	"html/template"
	"io"
	"strings"

	"github.com/jhillyerd/enmime"
	"github.com/k3a/html2text"
)

// DefaultMaxInlineImageSize is the largest image, in bytes, that WriteSingle embeds as a data: URI by default
const DefaultMaxInlineImageSize = 1 << 20

// DefaultMaxDepth is how deeply WriteSingle renders attached messages (message/rfc822) by default
const DefaultMaxDepth = 3

// remoteBlockingCSP is set inside each rendered HTML body unless remote content is allowed:
// images, styles and fonts may only come from the message itself (data: URIs), so opening it can't be tracked
const remoteBlockingCSP = "default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:"

// SingleOptions control WriteSingle. The zero value is safe to use.
type SingleOptions struct {
	//AllowRemoteContent lets the message's HTML load remote images, styles and fonts.
	//It is off by default, because remote content can reveal that the message was opened.
	AllowRemoteContent bool

	//MaxInlineImageSize is the largest image, in bytes, embedded as a data: URI: images the HTML refers to by cid:,
	//and image attachments shown in the attachment list. 0 means DefaultMaxInlineImageSize; negative disables embedding.
	MaxInlineImageSize int

	//MaxDepth limits how deeply attached messages (message/rfc822) are rendered. 0 means DefaultMaxDepth.
	MaxDepth int
}

func (o *SingleOptions) maxInline() int {
	if o == nil || o.MaxInlineImageSize == 0 {
		return DefaultMaxInlineImageSize
	}
	return o.MaxInlineImageSize
}

func (o *SingleOptions) maxDepth() int {
	if o == nil || o.MaxDepth <= 0 {
		return DefaultMaxDepth
	}
	return o.MaxDepth
}

func (o *SingleOptions) allowRemote() bool {
	return o != nil && o.AllowRemoteContent
}

var singleTmpl = template.Must(template.ParseFS(tmpls, "templates/single.tmpl"))

type singleAttachment struct {
	Name     string
	Type     string
	Size     string
	ImageURL template.URL
	Message  template.HTML
}

type singleMessage struct {
	Subject     string
	Date        string
	From        string
	To          string
	CC          string
	Bodies      []template.HTML
	Attachments []*singleAttachment
}

// WriteSingle parses the RFC 822 (eml) message read from r and writes one self-contained HTML document to w:
// the headers, the message body, and a list of attachments, with no other files.
//
// The message's own HTML is shown in a sandboxed iframe (srcdoc) that can't run scripts, submit forms or,
// unless opts.AllowRemoteContent is set, load anything from the network. Images it refers to by cid: are embedded as
// data: URIs. This makes the output suitable for showing untrusted mail inside another page, for example as the
// external renderer of a code forge (Forgejo, Gitea) for .eml files. opts may be nil.
func WriteSingle(w io.Writer, r io.Reader, opts *SingleOptions) error {
	msg, err := enmime.ReadEnvelope(r)
	if err != nil {
		return fmt.Errorf("Unable to parse msg: %w", err)
	}

	m, err := buildSingle(msg, opts, 1)
	if err != nil {
		return err
	}

	if err := singleTmpl.ExecuteTemplate(w, "page", m); err != nil {
		return fmt.Errorf("Unable to execute template: %w", err)
	}

	return nil
}

func buildSingle(msg *enmime.Envelope, opts *SingleOptions, depth int) (*singleMessage, error) {
	m := &singleMessage{
		Subject: msg.GetHeader("Subject"),
		Date:    msg.GetHeader("Date"),
		From:    msg.GetHeader("From"),
		To:      msg.GetHeader("To"),
		CC:      msg.GetHeader("CC"),
	}

	//images referenced by cid: become data: URIs
	contentIDMap := make(map[string]string)
	for _, p := range append(append([]*enmime.Part{}, msg.Inlines...), msg.Attachments...) {
		if p.ContentID == "" || !strings.HasPrefix(p.ContentType, "image/") || !fitsInline(len(p.Content), opts) {
			continue
		}
		contentIDMap[strings.Trim(p.ContentID, "<>")] = dataURI(p.ContentType, p.Content)
	}

	//the message's HTML, else its text
	for _, h := range findMainContent(msg.Root, ContentTypeTextHTML) {
		if strings.TrimSpace(html2text.HTML2Text(string(h))) == "" {
			continue
		}
		b, err := rewriteHTML(contentIDMap, h)
		if err != nil {
			b = h
		}
		m.Bodies = append(m.Bodies, sandboxedHTML(b, opts))
	}
	if len(m.Bodies) == 0 {
		for _, t := range findMainContent(msg.Root, ContentTypeTextPlain) {
			if strings.TrimSpace(string(t)) == "" {
				continue
			}
			m.Bodies = append(m.Bodies, template.HTML(`<pre class="text">`+html.EscapeString(string(t))+`</pre>`))
		}
	}

	//attachments, and inline parts that weren't embedded in the HTML (too big, or not images)
	for _, p := range msg.Attachments {
		a, err := singleAttachmentFor(p, opts, depth)
		if err != nil {
			return nil, err
		}
		m.Attachments = append(m.Attachments, a)
	}
	for _, p := range msg.Inlines {
		if _, embedded := contentIDMap[strings.Trim(p.ContentID, "<>")]; p.ContentID != "" && embedded {
			continue //already shown in the HTML
		}
		a, err := singleAttachmentFor(p, opts, depth)
		if err != nil {
			return nil, err
		}
		m.Attachments = append(m.Attachments, a)
	}

	return m, nil
}

func singleAttachmentFor(p *enmime.Part, opts *SingleOptions, depth int) (*singleAttachment, error) {
	a := &singleAttachment{Name: p.FileName, Type: p.ContentType, Size: formatSize(len(p.Content))}
	if a.Name == "" {
		a.Name = p.ContentType
	}

	switch {
	case p.ContentType == ContentTypeMessageRFC822 || p.FileName == "mime-attachment" || strings.HasPrefix(p.FileName, ".eml"):
		if depth >= opts.maxDepth() {
			return a, nil
		}
		sub, err := enmime.ReadEnvelope(bytes.NewReader(p.Content))
		if err != nil {
			return a, nil //not a parseable message: list it like any other attachment
		}
		sm, err := buildSingle(sub, opts, depth+1)
		if err != nil {
			return nil, err
		}
		buf := new(bytes.Buffer)
		if err := singleTmpl.ExecuteTemplate(buf, "message", sm); err != nil {
			return nil, fmt.Errorf("Unable to execute template: %w", err)
		}
		a.Message = template.HTML(buf.String())

	case strings.HasPrefix(p.ContentType, "image/") && fitsInline(len(p.Content), opts):
		a.ImageURL = template.URL(dataURI(p.ContentType, p.Content))
	}

	return a, nil
}

// sandboxedHTML wraps an HTML body in an iframe that can't run scripts, submit forms or reach its parent.
// Links open in a new tab. Unless remote content is allowed, a Content-Security-Policy blocks network loads.
func sandboxedHTML(b []byte, opts *SingleOptions) template.HTML {
	head := `<base target="_blank">`
	if !opts.allowRemote() {
		head = `<meta http-equiv="Content-Security-Policy" content="` + remoteBlockingCSP + `">` + head
	}
	doc := string(b)
	if i := strings.Index(strings.ToLower(doc), "<head>"); i >= 0 {
		doc = doc[:i+len("<head>")] + head + doc[i+len("<head>"):]
	} else {
		doc = head + doc
	}
	return template.HTML(`<iframe class="html" sandbox="allow-popups allow-popups-to-escape-sandbox" srcdoc="` +
		html.EscapeString(doc) + `"></iframe>`)
}

func fitsInline(size int, opts *SingleOptions) bool {
	max := opts.maxInline()
	return max > 0 && size <= max
}

func dataURI(contentType string, b []byte) string {
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func formatSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
