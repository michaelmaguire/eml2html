package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"strings"

	html "github.com/korylprince/eml2html"
	walk "github.com/korylprince/go-fs-walk"
)

func emlFilter(fi fs.FileInfo, path string, err error) error {
	if fi.IsDir() {
		return nil
	}
	if !strings.HasSuffix(fi.Name(), ".eml") {
		return walk.Skip
	}
	return nil
}

type file struct {
	r  io.Reader
	fi fs.FileInfo
}

func (f *file) Stat() (fs.FileInfo, error) {
	return f.fi, nil
}

func (f *file) Read(b []byte) (int, error) {
	return f.r.Read(b)
}

func (f *file) Close() error {
	return nil
}

func usage() {
	fmt.Printf("Usage: %s -in /path/to/in -out /path/to/out\n", os.Args[0])
	fmt.Printf("       %s -single [-in file.eml|-] [-out file.html|-]\n", os.Args[0])
	flag.PrintDefaults()
}

//writeSingle renders one message as one self-contained HTML document. An empty path or "-" means stdin/stdout.
func writeSingle(in, out string, opts *html.SingleOptions) error {
	var r io.Reader = os.Stdin
	if in != "" && in != "-" {
		f, err := os.Open(in)
		if err != nil {
			return fmt.Errorf("Unable to open input: %w", err)
		}
		defer f.Close()
		r = f
	}

	if out == "" || out == "-" {
		return html.WriteSingle(os.Stdout, r, opts)
	}

	buf := new(bytes.Buffer)
	if err := html.WriteSingle(buf, r, opts); err != nil {
		return err
	}
	if err := os.WriteFile(out, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("Unable to write output: %w", err)
	}
	return nil
}

func main() {
	input := flag.String("in", "", "input directory or file")
	output := flag.String("out", "", "output directory for html. Will be created if it doesn't exist")
	single := flag.Bool("single", false, "write one message as one self-contained HTML document. -in is an .eml file or - for stdin (the default), -out an .html file or - for stdout (the default)")
	remote := flag.Bool("remote", false, "with -single: let the message's HTML load remote images, styles and fonts (blocked by default)")
	maxInline := flag.Int("max-inline", html.DefaultMaxInlineImageSize, "with -single: largest image, in bytes, embedded as a data: URI; negative disables")
	maxDepth := flag.Int("max-depth", html.DefaultMaxDepth, "with -single: how deeply attached messages are rendered")
	sameOrigin := flag.Bool("same-origin", false, "with -single: give the message's frames the page's origin, so a script of the page can measure them (the message still can't run scripts); serve the output with a policy that allows no scripts")
	flag.Usage = usage
	flag.Parse()

	if *single {
		opts := &html.SingleOptions{AllowRemoteContent: *remote, MaxInlineImageSize: *maxInline, MaxDepth: *maxDepth,
			SameOriginFrames: *sameOrigin}
		if *maxInline == 0 {
			opts.MaxInlineImageSize = -1 // an explicit 0 means none, not the default
		}
		if err := writeSingle(*input, *output, opts); err != nil {
			log.Fatalln("Unable to render message:", err)
		}
		return
	}

	if *input == "" || *output == "" {
		usage()
		os.Exit(2)
	}

	root := path.Clean(*input)
	chroot := root

	c := walk.New(root)
	c.RegisterFilterFunc("eml", emlFilter)

	dirs := make(map[string][]fs.File)

	for f, p, err := c.Next(); err != io.EOF; f, p, err = c.Next() {
		if err != nil {
			log.Println("WARN: Error walking directory:", err)
			continue
		}

		// if -in is a file, chroot around it's parent
		if p == root && !f.IsDir() {
			chroot = path.Dir(p)
		}

		if f.IsDir() {
			// don't include root's parent if root is directory
			if p == root {
				continue
			}
			dirs[path.Dir(p)] = append(dirs[path.Dir(p)], &file{fi: f})
			continue
		}

		buf := new(bytes.Buffer)
		if _, err := io.Copy(buf, c); err != nil {
			log.Println("WARN: Error reading file:", err)
			continue
		}

		dirs[path.Dir(p)] = append(dirs[path.Dir(p)], &file{r: buf, fi: f})
	}

	if err := os.MkdirAll(*output, 0755); err != nil {
		log.Fatalln("Unable to create output directory:", err)
	}

	for d, entries := range dirs {
		var meta *html.Meta
		if d != root {
			meta = &html.Meta{Parent: "../index.html"}
		}
		if err := html.WriteDir(*output, path.Clean(strings.TrimPrefix(d, chroot)), entries, meta); err != nil {
			log.Fatalln("Unable to write directory:", err)
		}
	}
}
