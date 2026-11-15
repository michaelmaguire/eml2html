package eml2html

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"golang.org/x/net/html"
)

// inlineBaseStyle is the start of every inline body's shadow root: mail is written for a white page, and wide
// layouts scroll sideways rather than widen the page.
const inlineBaseStyle = `<style>:host{display:block;background:#fff;color:#000;overflow-x:auto}` +
	`.eml2html-body{padding:12px}img{max-width:100%;height:auto}</style>`

// dropped are removed with everything in them: they run code, load or embed other documents, or change how the
// page around them behaves.
var dropped = map[string]bool{
	"script": true, "noscript": true, "template": true,
	"iframe": true, "frame": true, "frameset": true, "object": true, "embed": true, "applet": true, "portal": true,
	"base": true, "meta": true, "link": true, "title": true,
	//SVG animation can set attributes, such as a link's href
	"set": true, "animate": true, "animatemotion": true, "animatetransform": true,
}

// unwrapped are removed, but what's in them stays.
var unwrapped = map[string]bool{"form": true, "html": true, "head": true, "body": true}

// urlAttrs hold URLs, which mustn't be scripts (javascript:) or documents (data: other than images).
var urlAttrs = map[string]bool{
	"href": true, "src": true, "action": true, "formaction": true, "background": true, "poster": true,
	"cite": true, "data": true, "lowsrc": true, "dynsrc": true, "longdesc": true, "usemap": true,
}

// droppedAttrs are removed whatever their value.
var droppedAttrs = map[string]bool{"srcdoc": true, "formaction": true, "action": true, "ping": true, "http-equiv": true}

// inlineHTML makes a message's HTML safe to put straight into a page, in a declarative shadow root (so its styles
// apply only to it): scripts, embedded documents, event handlers and script URLs are removed; links open in a new
// tab, and show where they go when hovered. The page still needs a Content-Security-Policy that allows no scripts
// and no remote content: removing them here is the second line, not the only one.
func inlineHTML(b []byte) (template.HTML, error) {
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("Unable to parse html: %w", err)
	}

	//the head's styles, then the body, as a div with the body's attributes
	var styles []*html.Node
	var body *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch strings.ToLower(c.Data) {
			case "head":
				for s := c.FirstChild; s != nil; s = s.NextSibling {
					if s.Type == html.ElementNode && strings.ToLower(s.Data) == "style" {
						styles = append(styles, s)
					}
				}
			case "body":
				body = c
			case "html":
				find(c)
			}
		}
	}
	find(doc)

	wrapper := &html.Node{Type: html.ElementNode, Data: "div"}
	if body != nil {
		wrapper.Attr = cleanAttrs("div", body.Attr)
		for c := body.FirstChild; c != nil; {
			next := c.NextSibling
			body.RemoveChild(c)
			wrapper.AppendChild(c)
			c = next
		}
	}
	wrapper.Attr = append(wrapper.Attr, html.Attribute{Key: "class", Val: "eml2html-body"})
	sanitize(wrapper)

	buf := new(bytes.Buffer)
	buf.WriteString(`<div class="inline"><template shadowrootmode="open">`)
	buf.WriteString(inlineBaseStyle)
	for _, s := range styles {
		if err := html.Render(buf, s); err != nil {
			return "", fmt.Errorf("Unable to render html: %w", err)
		}
	}
	if err := html.Render(buf, wrapper); err != nil {
		return "", fmt.Errorf("Unable to render html: %w", err)
	}
	buf.WriteString(`</template></div>`)
	return template.HTML(buf.String()), nil
}

// sanitize removes what inlineHTML doesn't allow from n's children, recursively.
func sanitize(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch c.Type {
		case html.CommentNode, html.DoctypeNode:
			n.RemoveChild(c)
		case html.ElementNode:
			name := strings.ToLower(c.Data)
			switch {
			case dropped[name]:
				n.RemoveChild(c)
			case unwrapped[name]:
				first := c.FirstChild
				for g := c.FirstChild; g != nil; {
					gnext := g.NextSibling
					c.RemoveChild(g)
					n.InsertBefore(g, c)
					g = gnext
				}
				n.RemoveChild(c)
				if first != nil {
					next = first //they're checked in turn
				}
			default:
				c.Attr = cleanAttrs(name, c.Attr)
				sanitize(c)
			}
		}
		c = next
	}
}

// cleanAttrs returns the attributes of a name element that inlineHTML allows.
func cleanAttrs(name string, attrs []html.Attribute) []html.Attribute {
	out := attrs[:0:0]
	href, hasTitle := "", false
	for _, a := range attrs {
		key := strings.ToLower(a.Key)
		if strings.HasPrefix(key, "on") || droppedAttrs[key] || key == "target" || key == "rel" {
			continue
		}
		if urlAttrs[key] && unsafeURL(a.Val) {
			continue
		}
		if key == "href" {
			href = a.Val
		}
		if key == "title" {
			hasTitle = true
		}
		out = append(out, a)
	}
	if name == "a" && href != "" {
		out = append(out, html.Attribute{Key: "target", Val: "_blank"}, html.Attribute{Key: "rel", Val: "noopener noreferrer"})
		if !hasTitle {
			out = append(out, html.Attribute{Key: "title", Val: href})
		}
	}
	return out
}

// unsafeURL reports whether a URL would run a script or open a document made by the message.
func unsafeURL(u string) bool {
	//browsers ignore whitespace and control characters inside the scheme
	var b strings.Builder
	for _, r := range u {
		if r > ' ' {
			b.WriteRune(r)
		}
	}
	v := strings.ToLower(b.String())
	switch {
	case strings.HasPrefix(v, "javascript:"), strings.HasPrefix(v, "vbscript:"):
		return true
	case strings.HasPrefix(v, "data:"):
		return !strings.HasPrefix(v, "data:image/")
	}
	return false
}

// inlineCSP is the Content-Security-Policy of a page with inline bodies: no scripts but scriptSrc, no plugins, no
// forms, and, unless remote content is allowed, nothing from the network.
func inlineCSP(opts *SingleOptions) string {
	scripts := "'none'"
	if opts.ScriptSrc != "" {
		scripts = opts.ScriptSrc
	}
	if opts.allowRemote() {
		return "default-src 'none'; img-src data: http: https:; style-src 'unsafe-inline' http: https:; " +
			"font-src data: http: https:; form-action 'none'; base-uri 'none'; script-src " + scripts
	}
	return "default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:; " +
		"form-action 'none'; base-uri 'none'; script-src " + scripts
}

// validScriptSrc reports whether s can go into a script-src directive as it is: sources only, no other directives.
func validScriptSrc(s string) bool {
	return !strings.ContainsAny(s, ";,\"<>\r\n")
}
