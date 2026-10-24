package main

import (
	"bytes"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// rewriteHTML applies the opt-in markup rewrites to an HTML document:
//
//	-relink   <link href="style.css">  ->  style.min.css
//	          <script src="app.js">    ->  app.min.js
//	-picture  <img src="photo.jpg" class="x">
//	            -> <picture><source srcset="photo.avif" type="image/avif">
//	                 <source srcset="photo.webp" type="image/webp">
//	                 <img src="photo.jpg" class="x"></picture>
//
// dir is the directory the finished document will live in. A reference is only
// rewritten when its replacement is actually on disk there, so cpwo can never
// point the page at a file it decided not to write; one of cpwo's own (own
// says which) must also not be older than the file it was made from. The <img>
// keeps every one of its attributes; only the wrapper is new.
func rewriteHTML(b []byte, dir string, relink, picture bool, own func(path string) bool) ([]byte, error) {
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}

	var imgs, refs []*html.Node
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.DataAtom {
			case atom.Img:
				// an <img> already inside a <picture> has its sources chosen already
				if n.Parent == nil || n.Parent.DataAtom != atom.Picture {
					imgs = append(imgs, n)
				}
			case atom.Link, atom.Script:
				refs = append(refs, n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(doc)

	if relink {
		for _, n := range refs {
			relinkNode(n, dir, own)
		}
	}
	if picture {
		for _, n := range imgs {
			wrapPicture(n, dir, own)
		}
	}

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// relinkNode points a <link href> or <script src> at its .min. twin.
func relinkNode(n *html.Node, dir string, own func(string) bool) {
	key, want := "href", ".css"
	if n.DataAtom == atom.Script {
		key, want = "src", ".js"
	}
	for i, a := range n.Attr {
		if !strings.EqualFold(a.Key, key) {
			continue
		}
		ref, suffix := splitRef(a.Val)
		ext := path.Ext(ref)
		if !isLocalRef(a.Val) || !strings.EqualFold(ext, want) || isGenerated(path.Base(ref)) {
			return
		}
		min := strings.TrimSuffix(ref, ext) + ".min" + ext
		if _, ok := current(dir, ref, min, own); !ok {
			return
		}
		n.Attr[i].Val = min + suffix
		return
	}
}

// wrapPicture puts <picture><source ...> around an <img>, one <source> per
// modern sidecar worth offering, best format first. No sidecar, no wrapper.
func wrapPicture(n *html.Node, dir string, own func(string) bool) {
	if keepImg(n) {
		return
	}
	src := ""
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, "src") {
			src = a.Val
		}
	}
	ref, suffix := splitRef(src)
	ext := path.Ext(ref)
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png":
	default:
		return
	}
	if !isLocalRef(src) {
		return
	}
	parent := n.Parent
	if parent == nil {
		return
	}

	// A sidecar is offered only if it beats everything a browser would
	// otherwise get, by the rule processImage writes them with: least preferred
	// first, each under the floor the previous one set. That also keeps out one
	// cpwo did not write, or one an earlier run left that loses now.
	floor := int64(math.MaxInt64)
	if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(ref))); err == nil {
		floor = fi.Size()
	}
	var sources []*html.Node
	for i := len(modern) - 1; i >= 0; i-- {
		f := modern[i]
		twin := strings.TrimSuffix(ref, ext) + f.ext
		fi, ok := current(dir, ref, twin, own)
		if !ok || fi.Size() >= floor {
			continue
		}
		floor = fi.Size()
		sources = append([]*html.Node{{
			Type: html.ElementNode, DataAtom: atom.Source, Data: "source",
			Attr: []html.Attribute{
				{Key: "srcset", Val: twin + suffix},
				{Key: "type", Val: f.mime},
			},
		}}, sources...)
	}
	if len(sources) == 0 {
		return
	}

	pic := &html.Node{Type: html.ElementNode, DataAtom: atom.Picture, Data: "picture"}
	parent.InsertBefore(pic, n)
	parent.RemoveChild(n)
	for _, s := range sources {
		pic.AppendChild(s)
	}
	pic.AppendChild(n)
}

// keepImg reports whether an <img> must stay exactly as it is. With a srcset the
// page picks a size per screen, and a single full-size <source> in front would
// win: a phone would download the 2000px picture instead of the 512px one. An
// image path in a data- attribute means a lazy-loader swaps it into src later,
// and a <source> in front would keep showing the placeholder for good.
func keepImg(n *html.Node) bool {
	for _, a := range n.Attr {
		key := strings.ToLower(a.Key)
		if key == "srcset" || (strings.HasPrefix(key, "data-") && mentionsImage(a.Val)) {
			return true
		}
	}
	return false
}

func mentionsImage(v string) bool {
	v = strings.ToLower(v)
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".svg"} {
		if strings.Contains(v, ext) {
			return true
		}
	}
	return false
}

// current reports whether twin is on disk next to the document and fit to
// point at. One of cpwo's own was refreshed by the first wave, so if it is
// still older than src, the file it was made from, it was left by a run with
// that group off and shows yesterday's version. Anybody else's twin, a
// vendor's lib.min.css say, only has to exist: its mtime says when it was
// unpacked, not whether it matches.
func current(dir, src, twin string, own func(string) bool) (os.FileInfo, bool) {
	tp := filepath.Join(dir, filepath.FromSlash(twin))
	ti, err := os.Stat(tp)
	if err != nil {
		return nil, false
	}
	if own(tp) {
		si, err := os.Stat(filepath.Join(dir, filepath.FromSlash(src)))
		if err == nil && ti.ModTime().Before(si.ModTime()) {
			return nil, false
		}
	}
	return ti, true
}

// splitRef separates a reference from its ?query or #fragment.
func splitRef(v string) (ref, suffix string) {
	if i := strings.IndexAny(v, "?#"); i >= 0 {
		return v[:i], v[i:]
	}
	return v, ""
}

// isLocalRef reports whether v is a plain relative path cpwo can resolve on
// disk: not empty, not a URL, not a data: blob, not site-root absolute.
func isLocalRef(v string) bool {
	if v == "" || strings.HasPrefix(v, "/") || strings.HasPrefix(v, "#") {
		return false
	}
	if i := strings.Index(v, ":"); i >= 0 {
		if j := strings.IndexAny(v, "/?#"); j < 0 || i < j {
			return false // has a scheme, e.g. https: or data:
		}
	}
	return true
}
