[![pkg.go.dev](https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white&style=flat-square)](https://pkg.go.dev/github.com/korylprince/eml2html)

# About

`eml2html` is a library and command line tool used to transform RFC 822 (eml) files into html files viewable in a browser.


# Installing

Library, using Go Modules:

`go get github.com/korylprince/eml2html`


CLI Tool:

``` bash
mkdir build
cd build
go mod init build
go get -d github.com/korylprince/eml2html/cmd/eml2html@<version>
go build github.com/korylprince/eml2html/cmd/eml2html
./eml2html -h
```

# Usage

## A directory of messages

```bash
eml2html -in /path/to/mail -out /path/to/html
```

Each message becomes a folder with an `index.html`, its content and its attachments, and each folder of messages gets an `index.html` listing them.

## One message, as one HTML document

```bash
eml2html -single -in message.eml -out message.html
```

`-in` and `-out` default to stdin and stdout (`-` also means stdin/stdout), so it works in a pipeline:

```bash
eml2html -single < message.eml > message.html
```

The output is one self-contained file: the headers, the message body, and a list of attachments, with nothing else to copy around. It is meant to be safe for showing untrusted mail inside another page:

* The message's own HTML is shown in a sandboxed `<iframe srcdoc>`, which can't run scripts, submit forms or reach the page around it. Its links open in a new tab.
* Remote images, styles and fonts are blocked (a Content-Security-Policy inside the frame), so opening a message can't be tracked. `-remote` allows them.
* Images the HTML refers to by `cid:` are embedded as `data:` URIs, as are image attachments, up to `-max-inline` bytes each (default 1 MiB; negative disables). Larger ones are listed with their name, type and size.
* Attached messages (`message/rfc822`) are rendered inside the page, up to `-max-depth` levels (default 3).
* `-inline` puts the message's HTML into the page itself rather than a frame (see below: for forges that sandbox the whole page).
* The frames have a fixed height (resizable), since without scripts a frame can't fit its content. A page that embeds the output and wants to fit them with its own script can pass `-same-origin`: the frames then get the page's origin (`sandbox="allow-same-origin ..."`), so the script can measure them. The message still can't run scripts, but its HTML then shares the page's origin, so a browser flaw in sandboxing would expose that origin: serve the output with a policy that allows no scripts either (for example `Content-Security-Policy: sandbox allow-same-origin allow-popups; default-src 'none'; style-src 'unsafe-inline'; img-src data:`).

The library equivalent is `eml2html.WriteSingle(w, r, opts)`.

### As a renderer for .eml files in Forgejo or Gitea

Forgejo and Gitea can show files with an external renderer. With `RENDER_CONTENT_MODE = iframe`, the forge serves the renderer's output from its own route, in a frame sandboxed to a unique origin (`Content-Security-Policy: sandbox allow-scripts`), so it can't reach the forge's pages or session. The forge also appends a small script that tells the file page the output's height, and sizes the frame to fit, once, when it loads.

Inside that sandbox, a further frame for the message can't be measured, so the message would scroll inside the page. `-inline` puts the message's HTML straight into the page instead, so the frame fits the whole message. That's safe there because the forge's sandbox isolates the page, and on top of it the message's HTML is cleaned (no scripts, embedded documents, event handlers or script URLs) and kept apart in a shadow root, and the page's own Content-Security-Policy allows no scripts but the forge's, and nothing remote. With a statically built `eml2html` on the server, add to `app.ini`:

```ini
[markup.eml]
ENABLED = true
FILE_EXTENSIONS = .eml
RENDER_COMMAND = /usr/local/bin/eml2html -single -inline -script-src='sha256-n7qTMI5/xztyviX2GLQ9r4UfLwHhoKsoPKEvC5/M11Q='
IS_INPUT_FILE = false
RENDER_CONTENT_MODE = iframe
NEED_POSTPROCESS = false
```

* `-script-src` allows the forge's height script by its hash. The hash above is of Forgejo 15's (`modules/markup/renderer.go`); if a forge version changes that script, the frame stays at its initial 300 px until the hash is updated. Work it out from the script's text, between `<script>` and `</script>`: `printf '%s' '<script text>' | openssl dgst -sha256 -binary | base64`.
* `NEED_POSTPROCESS = false`: otherwise the forge rewrites the output as it does Markdown (issue references, mentions, links).
* `RENDER_COMMAND` is split on spaces, with no shell, so the quotes in `-script-src='sha256-...'` reach eml2html as they are, as the policy needs.
* Links in the message don't open: the forge's sandbox doesn't allow popups or navigating the page. Hovering one shows where it goes.
* The forge shows files up to `[ui] MAX_DISPLAY_FILE_SIZE` (8 MiB by default); bigger ones can only be downloaded.
* `-inline` needs a browser with declarative shadow DOM (2023 on, Firefox from 123): an older one shows the headers and attachments but not the message's HTML.

Without `-inline` (`-single` alone), the message's HTML is in a fixed-height frame (resizable) inside the page.


# Development

The Makefile runs everything in a container (see `Containerfile`), so only Podman (or Docker: `make test ENGINE=docker`) is needed:

```bash
make test        # go test -race ./...
make vet         # go vet ./...
make build       # static binary in ./bin/eml2html
make shell       # a shell in the dev container
```


If you have any issues or questions [create an issue](https://github.com/korylprince/eml2html/issues).
