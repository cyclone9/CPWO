package main

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func allOn() map[string]bool {
	on := make(map[string]bool, len(groups))
	for _, g := range groups {
		on[g] = true
	}
	return on
}

// TestSidecar is the default mode: outputs land next to their sources, sources
// are untouched, and a second run finds nothing left to do.
func TestSidecar(t *testing.T) {
	src := t.TempDir()
	putFile(t, filepath.Join(src, "index.html"),
		"<!doctype html>\n<html>\n  <head>\n    <title>hi</title>\n  </head>\n  <body>   <p>hello</p>   </body>\n</html>\n")
	putFile(t, filepath.Join(src, "assets", "style.css"), "body {\n    color: #ffffff;\n    margin: 0px;\n}\n")
	putFile(t, filepath.Join(src, "app.js"), "function add(a, b) {\n    // sum\n    return a + b;\n}\n")
	putFile(t, filepath.Join(src, "node_modules", "junk.css"), "a{color:red}")
	writePNG(t, filepath.Join(src, "pic.png"), 256)

	o := options{quality: 82, jobs: 4, on: allOn(), sizes: []int{512}}
	s, err := run([]string{src}, o)
	if err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{"index.min.html", filepath.Join("assets", "style.min.css"), "app.min.js"} {
		orig := filepath.Join(src, replaceMin(rel))
		if size(t, filepath.Join(src, rel)) >= size(t, orig) {
			t.Errorf("%s is not smaller than its source", rel)
		}
	}
	if size(t, filepath.Join(src, "pic.webp")) == 0 {
		t.Error("pic.webp missing")
	}
	if _, err := os.Stat(filepath.Join(src, "node_modules", "junk.min.css")); err == nil {
		t.Error("node_modules was processed")
	}
	if s.scanned != 4 {
		t.Errorf("scanned %d files, want 4", s.scanned)
	}

	// Second run: everything is already up to date and nothing gets double-minified.
	s2, err := run([]string{src}, o)
	if err != nil {
		t.Fatal(err)
	}
	if s2.written != 0 {
		t.Errorf("second run wrote %d files, want 0", s2.written)
	}
	if s2.scanned != s.scanned {
		t.Errorf("second run scanned %d, want %d (generated files must be ignored)", s2.scanned, s.scanned)
	}
	if s2.in != 0 || s2.out != 0 || s2.webps != 0 {
		t.Errorf("second run totals %d -> %d (+%d webp), want all zero: skipped files must not be counted",
			s2.in, s2.out, s2.webps)
	}
	if _, err := os.Stat(filepath.Join(src, "index.min.min.html")); err == nil {
		t.Error("double-minified file produced on re-run")
	}

	// -overwrite forces the work again.
	o.overwrite = true
	s3, err := run([]string{src}, o)
	if err != nil {
		t.Fatal(err)
	}
	if s3.written == 0 {
		t.Error("-overwrite wrote nothing")
	}

	// Every write goes through a temporary file that is renamed into place;
	// none may be left behind.
	filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".cpwo-") && strings.HasSuffix(d.Name(), ".tmp") {
			t.Errorf("temporary file left behind: %s", p)
		}
		return nil
	})
}

// TestOnlyWebP covers `cpwo -webp <path>`: images only, nothing else touched.
func TestOnlyWebP(t *testing.T) {
	src := t.TempDir()
	putFile(t, filepath.Join(src, "style.css"), "body {\n  color: #ffffff;\n}\n")
	writePNG(t, filepath.Join(src, "pic.png"), 256)

	_, err := run([]string{src}, options{quality: 82, jobs: 2, on: map[string]bool{"webp": true}})
	if err != nil {
		t.Fatal(err)
	}
	if size(t, filepath.Join(src, "pic.webp")) == 0 {
		t.Error("pic.webp missing")
	}
	if _, err := os.Stat(filepath.Join(src, "style.min.css")); err == nil {
		t.Error("css was minified although only -webp was asked for")
	}
	if _, err := os.Stat(filepath.Join(src, "pic.min.png")); err == nil {
		t.Error("png was recompressed although only -webp was asked for")
	}
}

// TestResizeNeverUpscales: a 256px wide source gets a 64px variant, no 512px one.
func TestResize(t *testing.T) {
	src := t.TempDir()
	writePNG(t, filepath.Join(src, "pic.png"), 256)

	s, err := run([]string{src}, options{
		quality: 82, jobs: 1, resize: true, sizes: []int{512, 128, 64},
		on: map[string]bool{"webp": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.variants != 2 {
		t.Errorf("made %d variants, want 2 (512 must be skipped, not upscaled)", s.variants)
	}
	if _, err := os.Stat(filepath.Join(src, "pic-512.webp")); err == nil {
		t.Error("pic-512.webp was created from a 256px source")
	}
	for _, w := range []string{"pic-128.webp", "pic-64.webp"} {
		if size(t, filepath.Join(src, w)) == 0 {
			t.Errorf("%s missing or empty", w)
		}
	}
}

// TestManifest: .cpwo-output must list every generated file — including ones a
// re-run skipped as up to date, since it describes the output set that exists,
// not just the latest delta — and nothing that cpwo did not create.
func TestManifest(t *testing.T) {
	src := t.TempDir()
	putFile(t, filepath.Join(src, "index.html"), "<!doctype html><html><body>  <p>hi</p>  </body></html>")
	putFile(t, filepath.Join(src, "assets", "style.css"), "body {\n  color: #ffffff;\n}\n")
	putFile(t, filepath.Join(src, "assets", "vendor.min.css"), "a{color:red}")
	putFile(t, filepath.Join(src, "notes.txt"), "not mine")
	writePNG(t, filepath.Join(src, "pic.png"), 256)

	o := options{quality: 82, jobs: 4, on: allOn()}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	first := string(read(t, filepath.Join(src, manifestName)))

	for _, want := range []string{
		"index.html", "index.min.html",
		"assets/style.css", "assets/style.min.css",
		"pic.png", "pic.webp",
		"# File Map", "# File Tree",
		"┌", "│", "└──", // table and tree actually rendered
	} {
		if !strings.Contains(first, want) {
			t.Errorf("manifest is missing %q:\n%s", want, first)
		}
	}
	for _, unwanted := range []string{"notes.txt", "vendor.min.css", manifestName + "\n"} {
		if strings.Contains(first, unwanted) {
			t.Errorf("manifest lists %q, which cpwo did not generate:\n%s", unwanted, first)
		}
	}
	// forward slashes everywhere, so the file reads the same on every platform
	if strings.Contains(first, `assets\`) {
		t.Errorf("manifest uses backslashes:\n%s", first)
	}

	// A second run writes nothing but must still describe the same output set.
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	second := string(read(t, filepath.Join(src, manifestName)))
	for _, want := range []string{"index.min.html", "assets/style.min.css", "pic.webp"} {
		if !strings.Contains(second, want) {
			t.Errorf("re-run dropped %q from the manifest:\n%s", want, second)
		}
	}
}

func TestRenderTree(t *testing.T) {
	got := render([]entry{
		{src: "index.html", outs: []string{"index.min.html"}, in: 100, out: 60},
		{src: "assets/a.css", outs: []string{"assets/a.min.css"}, in: 100, out: 40},
		{src: "assets/img/p.png", outs: []string{"assets/img/p.webp"}, in: 100, out: 50},
	})
	for _, want := range []string{
		"Written by CPWO v",
		"Result: 3 sources -> 3 generated files",
		"assets/a.css",
		"┌", "┬", "┐", "├", "┼", "┤", "└", "┴", "┘", // table box
		"├── ", "└── ", "│   ", // tree box
		"index.min.html",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// directories sort before files at each level of the tree
	tree := got[strings.Index(got, "# File Tree"):]
	if strings.Index(tree, "├── assets") > strings.Index(tree, "└── index.min.html") {
		t.Errorf("files listed before directories:\n%s", tree)
	}
	// every table row is the same width, or the box does not close
	table := got[strings.Index(got, "# File Map"):strings.Index(got, "# File Tree")]
	var w int
	for _, line := range strings.Split(strings.TrimSpace(table), "\n") {
		if !strings.ContainsAny(line, "│┌├└") {
			continue
		}
		if n := width(line); w == 0 {
			w = n
		} else if n != w {
			t.Fatalf("table row is %d wide, want %d:\n%s", n, w, table)
		}
	}
	if w == 0 {
		t.Fatalf("no table rows found in:\n%s", table)
	}
}

// TestResolveGroups pins the flag semantics: naming groups means "only these",
// switching one off means "everything but this".
func TestResolveGroups(t *testing.T) {
	vals := func(m map[string]bool) map[string]*bool {
		out := make(map[string]*bool, len(groups))
		for _, g := range groups {
			v := true
			if b, ok := m[g]; ok {
				v = b
			}
			out[g] = &v
		}
		return out
	}

	on := resolveGroups(map[string]bool{"webp": true}, vals(nil), false)
	if !on["webp"] || on["css"] || len(on) != 1 {
		t.Errorf("-webp gave %v, want only webp", on)
	}

	on = resolveGroups(map[string]bool{"css": true, "js": true}, vals(nil), false)
	if !on["css"] || !on["js"] || on["html"] {
		t.Errorf("-css -js gave %v, want only css and js", on)
	}

	on = resolveGroups(map[string]bool{"js": true}, vals(map[string]bool{"js": false}), false)
	if on["js"] || !on["css"] {
		t.Errorf("-js=false gave %v, want everything except js", on)
	}

	on = resolveGroups(nil, vals(nil), false)
	for _, g := range groups {
		if !on[g] {
			t.Errorf("no flags: %s should be on", g)
		}
	}

	// A default-off group is the only one you have to name to get at all, so
	// naming it must not narrow the run the way -webp does.
	on = resolveGroups(map[string]bool{"avif": true}, vals(map[string]bool{"avif": true}), false)
	if !on["avif"] || !on["css"] || !on["webp"] {
		t.Errorf("-avif gave %v, want the default run plus avif", on)
	}

	on = resolveGroups(map[string]bool{"webp": true, "avif": true}, vals(nil), false)
	if !on["webp"] || !on["avif"] || on["css"] || len(on) != 2 {
		t.Errorf("-webp -avif gave %v, want only webp and avif", on)
	}

	on = resolveGroups(map[string]bool{"avif": true, "js": true},
		vals(map[string]bool{"avif": true, "js": false}), false)
	if !on["avif"] || on["js"] || !on["css"] {
		t.Errorf("-avif -js=false gave %v, want the default run plus avif, minus js", on)
	}

	// -only is what gets a default-off group by itself.
	on = resolveGroups(map[string]bool{"avif": true}, vals(map[string]bool{"avif": true}), true)
	if !on["avif"] || len(on) != 1 {
		t.Errorf("-avif -only gave %v, want only avif", on)
	}

	// main turns an empty set into an error rather than a silent no-op run.
	if on := resolveGroups(nil, vals(nil), true); len(on) != 0 {
		t.Errorf("-only with nothing named gave %v, want empty", on)
	}
}

// TestHTMLKeepsDefaultAttrs guards the one HTML transformation that silently
// breaks real pages: dropping type="text" unmatches every input[type="text"]
// CSS rule and querySelector.
func TestHTMLKeepsDefaultAttrs(t *testing.T) {
	src := t.TempDir()
	putFile(t, filepath.Join(src, "f.html"),
		"<!doctype html>\n<html>\n<body>\n  <input type=\"text\" id=\"a\">\n  <!-- gone -->\n</body>\n</html>\n")

	if _, err := run([]string{src}, options{quality: 82, jobs: 1, on: map[string]bool{"html": true}}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(src, "f.min.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("type=text")) && !bytes.Contains(got, []byte(`type="text"`)) {
		t.Errorf("type=\"text\" was stripped: %s", got)
	}
	if bytes.Contains(got, []byte("gone")) {
		t.Errorf("comment survived minification: %s", got)
	}
}

// TestRelinkAndPicture covers the two markup rewrites end to end, including the
// ordering guarantee: the HTML wave runs after the assets it points at exist.
func TestRelinkAndPicture(t *testing.T) {
	src := t.TempDir()
	putFile(t, filepath.Join(src, "index.html"), `<!doctype html><html><head>
<link rel="stylesheet" href="assets/style.css">
<link rel="stylesheet" href="assets/vendor.min.css">
<link rel="stylesheet" href="https://cdn.example.com/x.css">
<link rel="icon" href="pic.png">
</head><body>
<img src="pic.png" class="logo" width="46" height="46" alt="" loading="lazy">
<img src="photo.png" alt="photo">
<img src="missing.png" alt="no twin">
<img src="https://example.com/remote.jpg" alt="remote">
<picture><img src="pic.png" alt="already wrapped"></picture>
<script src="assets/app.js"></script>
</body></html>`)
	putFile(t, filepath.Join(src, "assets", "style.css"), "body {\n  color: #ffffff;\n}\n")
	putFile(t, filepath.Join(src, "assets", "vendor.min.css"), "a{color:red}")
	putFile(t, filepath.Join(src, "assets", "app.js"), "function f(a, b) {\n  return a + b;\n}\n")
	writePNG(t, filepath.Join(src, "pic.png"), 256)
	writeImage(t, filepath.Join(src, "photo.png"), photoImage(256))

	o := options{quality: 82, jobs: 4, on: allOn(), relink: true, picture: true}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	got := string(read(t, filepath.Join(src, "index.min.html")))

	for _, want := range []string{
		`href=assets/style.min.css`,     // relinked
		`src=assets/app.min.js`,         // relinked
		`href=assets/vendor.min.css`,    // already minified, left alone
		`https://cdn.example.com/x.css`, // external, left alone
		// AVIF first: the browser takes the first <source> it understands, so
		// the order in `modern` is what decides which format actually ships.
		`<source srcset=photo.avif type=image/avif><source srcset=photo.webp type=image/webp>`,
		// pic.png is static, which AVIF compresses worse than WebP does, so no
		// .avif was written and none may be offered.
		`<picture><source srcset=pic.webp type=image/webp><img src=pic.png`,
		`class=logo`, `width=46`, `height=46`, `loading=lazy`, // img attrs survive
		`src=missing.png`,                // no .webp twin, left alone
		`https://example.com/remote.jpg`, // external, left alone
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "pic.avif") {
		t.Error("offered pic.avif, which lost to pic.webp and was never written")
	}
	if n := strings.Count(got, "<picture>"); n != 3 {
		t.Errorf("got %d <picture> elements, want 3 (two new, one pre-existing not double-wrapped)", n)
	}
	if strings.Contains(got, "href=pic.min.png") {
		t.Error("rel=icon href was relinked; only .css and .js should be")
	}
}

// TestRewriteIgnoresUpToDate: a rewrite depends on which sibling files exist,
// not on this file's mtime, so adding -relink to an already-processed tree must
// take effect without -overwrite.
func TestRewriteIgnoresUpToDate(t *testing.T) {
	src := t.TempDir()
	putFile(t, filepath.Join(src, "index.html"),
		"<!doctype html><html><head><link rel=\"stylesheet\" href=\"style.css\"></head><body>x</body></html>")
	putFile(t, filepath.Join(src, "style.css"), "body {\n  color: #ffffff;\n}\n")

	plain := options{quality: 82, jobs: 2, on: allOn()}
	if _, err := run([]string{src}, plain); err != nil {
		t.Fatal(err)
	}
	if got := string(read(t, filepath.Join(src, "index.min.html"))); strings.Contains(got, "style.min.css") {
		t.Fatalf("relink happened without the flag: %s", got)
	}

	withRelink := plain
	withRelink.relink = true
	s, err := run([]string{src}, withRelink)
	if err != nil {
		t.Fatal(err)
	}
	got := string(read(t, filepath.Join(src, "index.min.html")))
	if !strings.Contains(got, "style.min.css") {
		t.Errorf("-relink was skipped as up to date, needs no -overwrite: %s", got)
	}
	if s.written == 0 {
		t.Error("nothing written on the -relink run")
	}
}

// TestResizeFallback: -resize-fallback adds same-format variants, and naming
// them .min. keeps a re-run from cascading photo-512.png into photo-512-128.png.
func TestResizeFallback(t *testing.T) {
	src := t.TempDir()
	writePNG(t, filepath.Join(src, "pic.png"), 256)

	o := options{quality: 82, jobs: 1, resize: true, resizeFallback: true,
		sizes: []int{128, 64}, on: allOn()}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"pic-128.webp", "pic-64.webp", "pic-128.min.png", "pic-64.min.png"} {
		if size(t, filepath.Join(src, f)) == 0 {
			t.Errorf("%s missing or empty", f)
		}
	}

	before, _ := os.ReadDir(src)
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadDir(src)
	if len(after) != len(before) {
		var names []string
		for _, e := range after {
			names = append(names, e.Name())
		}
		t.Errorf("re-run went from %d to %d files, want no cascade: %v", len(before), len(after), names)
	}
}

// TestOnlyAVIF covers `cpwo -avif <path>`, and with it the fact that AVIF is
// off unless asked for: a default run must not write one.
func TestOnlyAVIF(t *testing.T) {
	src := t.TempDir()
	writePNG(t, filepath.Join(src, "pic.png"), 256)

	def := allOn()
	delete(def, "avif") // what `cpwo <path>` resolves to
	if _, err := run([]string{src}, options{quality: 82, jobs: 2, on: def}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(src, "pic.avif")); err == nil {
		t.Error("pic.avif written without -avif; AVIF must stay opt-in")
	}

	s, err := run([]string{src}, options{quality: 82, jobs: 2, on: map[string]bool{"avif": true}})
	if err != nil {
		t.Fatal(err)
	}
	if size(t, filepath.Join(src, "pic.avif")) == 0 {
		t.Error("pic.avif missing")
	}
	if s.avifs != 1 {
		t.Errorf("counted %d avifs, want 1", s.avifs)
	}
}

// TestAVIFQualityScale: AVIF's quality numbers are not JPEG's. Handing the
// encoder a bare -q 82 produces a file bigger than the WebP it is supposed to
// beat, which is the whole reason avifQuality exists.
func TestAVIFQualityScale(t *testing.T) {
	img := photoImage(512)
	av, err := bestAVIF(img, false, 82)
	if err != nil {
		t.Fatal(err)
	}
	wb, err := bestWebP(img, false, 82)
	if err != nil {
		t.Fatal(err)
	}
	if len(av) >= len(wb) {
		t.Errorf("avif %d B, webp %d B at -q 82: avif should win, is -q still being scaled?",
			len(av), len(wb))
	}
	if q := avifQuality(100); q != 100 {
		t.Errorf("avifQuality(100) = %d, want 100", q)
	}
}

// TestBestWebP: a flat-colour logo must go out lossless, which is what stops
// cpwo emitting a .webp several KB bigger than the .png it came from.
func TestBestWebP(t *testing.T) {
	flat := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			flat.Set(x, y, color.RGBA{0xF7, 0x93, 0x1A, 0xFF})
		}
	}
	lossy, err := bestWebP(flat, false, 82)
	if err != nil {
		t.Fatal(err)
	}
	best, err := bestWebP(flat, true, 82)
	if err != nil {
		t.Fatal(err)
	}
	if len(best) >= len(lossy) {
		t.Errorf("png path gave %d bytes, lossy-only gave %d: lossless should win on flat colour",
			len(best), len(lossy))
	}
}

func TestParseSizes(t *testing.T) {
	got, err := parseSizes(" 256, 1024 ,512 ")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{1024, 512, 256}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (descending)", got, want)
		}
	}
	if _, err := parseSizes("512,abc"); err == nil {
		t.Error("expected an error for a non-numeric size")
	}
	if _, err := parseSizes("0"); err == nil {
		t.Error("expected an error for a zero size")
	}
	if got, _ := parseSizes("512,256,512"); !slices.Equal(got, []int{512, 256}) {
		t.Errorf("got %v, want [512 256]: a width named twice is decided once", got)
	}
}

// TestParseArgs: flags work after paths too, so `cpwo ./site -avif` does not
// go looking for a folder called -avif, and a bare -- still ends the flags.
func TestParseArgs(t *testing.T) {
	c, err := parseArgs([]string{"./site", "-avif", "-q", "70", "other", "--", "-odd"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.paths, []string{"./site", "other", "-odd"}) {
		t.Errorf("paths %q, want [./site other -odd]", c.paths)
	}
	if c.o.quality != 70 || !c.o.on["avif"] || !c.o.on["css"] {
		t.Errorf("flags after a path were not applied: q=%d on=%v", c.o.quality, c.o.on)
	}

	if c, _ = parseArgs([]string{"-sizes", "512,256", "x"}, io.Discard); !c.o.resize {
		t.Error("-sizes should imply -r")
	}
	if c, _ = parseArgs([]string{"-resize-fallback", "x"}, io.Discard); !c.o.resize {
		t.Error("-resize-fallback should imply -r")
	}
	if c, _ = parseArgs([]string{"-license"}, io.Discard); !c.license {
		t.Error("-license not recognised")
	}
	if _, err := parseArgs([]string{"-q", "0", "x"}, io.Discard); err == nil {
		t.Error("-q 0 accepted")
	}
	if _, err := parseArgs([]string{"-nope", "x"}, io.Discard); !errors.Is(err, errUsage) {
		t.Errorf("unknown flag gave %v, want errUsage", err)
	}
}

// TestPictureLeavesResponsiveAndLazyImages: a single full-size <source> would
// beat an <img srcset> on every screen, and would hide whatever a lazy-loader
// later swaps into src. Only the plain <img> gets a <picture>.
func TestPictureLeavesResponsiveAndLazyImages(t *testing.T) {
	src := t.TempDir()
	writePNG(t, filepath.Join(src, "pic.png"), 256)
	putFile(t, filepath.Join(src, "index.html"), `<!doctype html><html><body>
<img src="pic.png" srcset="pic-128.min.png 128w, pic.png 256w" sizes="50vw" alt="responsive">
<img src="pic.png" data-src="real.jpg" class="lazyload" alt="lazy">
<img src="pic.png" alt="plain">
</body></html>`)

	o := options{quality: 82, jobs: 2, on: map[string]bool{"html": true, "webp": true}, picture: true}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	got := string(read(t, filepath.Join(src, "index.min.html")))
	if n := strings.Count(got, "<picture>"); n != 1 {
		t.Errorf("got %d <picture> elements, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "<img src=pic.png alt=plain></picture>") {
		t.Errorf("the plain <img> was not wrapped:\n%s", got)
	}
}

// TestOutdatedSidecarsAreRemoved: the size rule binds files from earlier runs
// as well. An .avif written when there was no .webp to beat must go once a
// smaller .webp exists, and a .webp of a picture that has since been replaced
// must not outlive the new one losing to its source. Either would otherwise
// be offered by <picture>.
func TestOutdatedSidecarsAreRemoved(t *testing.T) {
	src := t.TempDir()
	writePNG(t, filepath.Join(src, "pic.png"), 256) // static: AVIF loses to WebP on it
	avifOnly := options{quality: 82, jobs: 2, on: map[string]bool{"avif": true}}
	if _, err := run([]string{src}, avifOnly); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(src, "pic.avif")) {
		t.Fatal("an AVIF-only run wrote no pic.avif; the fixture no longer sets up the case")
	}
	both := options{quality: 82, jobs: 2, on: map[string]bool{"webp": true, "avif": true}}
	if _, err := run([]string{src}, both); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(src, "pic.webp")) || exists(filepath.Join(src, "pic.avif")) {
		t.Error("pic.avif survived although pic.webp beats it")
	}

	photo := filepath.Join(src, "photo.jpg")
	writeJPEG(t, photo, photoImage(256), 92)
	webpOnly := options{quality: 82, jobs: 2, on: map[string]bool{"webp": true}}
	if _, err := run([]string{src}, webpOnly); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(src, "photo.webp")) {
		t.Fatal("no photo.webp for the first photo")
	}
	// a different picture, saved so small that no WebP can beat it
	writeJPEG(t, photo, noisyImage(256), 3)
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(photo, later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := run([]string{src}, webpOnly); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(src, "photo.webp")) {
		t.Error("photo.webp of the replaced picture survived")
	}
	if man := string(read(t, filepath.Join(src, manifestName))); strings.Contains(man, "photo.webp") ||
		strings.Contains(man, "pic.avif") {
		t.Errorf("manifest still lists removed files:\n%s", man)
	}
}

// TestLeavesFilesItDidNotMake: a vendor drop ships lib.css and lib.min.css side
// by side, and a .webp may have been exported by hand. Neither is cpwo's, so
// neither is overwritten or listed, not even with -overwrite.
func TestLeavesFilesItDidNotMake(t *testing.T) {
	src := t.TempDir()
	vendor := "/*! Vendor | MIT */.a{color:red}\n/*# sourceMappingURL=lib.min.css.map */\n"
	putFile(t, filepath.Join(src, "lib.css"), "/*! Vendor | MIT */\n.a {\n  color: red;\n}\n")
	putFile(t, filepath.Join(src, "lib.min.css"), vendor)
	old := time.Now().Add(-time.Hour) // older than lib.css, as an unzip can leave it
	if err := os.Chtimes(filepath.Join(src, "lib.min.css"), old, old); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(src, "pic.png"), 64)
	// bigger than the PNG, so it loses the size rule and cpwo would remove it
	// if it took it for its own
	handMade := strings.Repeat("exported by hand ", 4000)
	putFile(t, filepath.Join(src, "pic.webp"), handMade)
	putFile(t, filepath.Join(src, "app.js"), "function f(a, b) {\n  return a + b;\n}\n")

	for _, overwrite := range []bool{false, true} {
		o := options{quality: 82, jobs: 2, on: allOn(), overwrite: overwrite}
		s, err := run([]string{src}, o)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(read(t, filepath.Join(src, "lib.min.css"))); got != vendor {
			t.Errorf("overwrite=%v: the vendor's lib.min.css was replaced with %q", overwrite, got)
		}
		if !exists(filepath.Join(src, "pic.webp")) || string(read(t, filepath.Join(src, "pic.webp"))) != handMade {
			t.Errorf("overwrite=%v: the hand-made pic.webp was replaced or removed", overwrite)
		}
		if s.left < 2 {
			t.Errorf("overwrite=%v: %d outputs left alone, want at least 2", overwrite, s.left)
		}
		man := string(read(t, filepath.Join(src, manifestName)))
		if strings.Contains(man, "lib.min.css") || strings.Contains(man, "pic.webp") {
			t.Errorf("overwrite=%v: manifest claims files cpwo did not make:\n%s", overwrite, man)
		}
	}
}

// TestContestedOutputGoesToFirstSource: photo.jpg and photo.png both map to
// photo.webp. The first in walk order gets it, on every run, and the manifest
// lists it once. The re-run matters: by then photo.webp is a listed cpwo file,
// and only the claim stops photo.png from taking it over.
func TestContestedOutputGoesToFirstSource(t *testing.T) {
	src := t.TempDir()
	writeJPEG(t, filepath.Join(src, "photo.jpg"), photoImage(96), 92)
	writeImage(t, filepath.Join(src, "photo.png"), noisyImage(64))

	o := options{quality: 82, jobs: 4, on: map[string]bool{"webp": true}}
	for i := 1; i <= 2; i++ {
		s, err := run([]string{src}, o)
		if err != nil {
			t.Fatal(err)
		}
		if cfg := decodeConfig(t, filepath.Join(src, "photo.webp")); cfg.Width != 96 {
			t.Errorf("run %d: photo.webp is %dpx wide, want 96: it must come from photo.jpg", i, cfg.Width)
		}
		if s.left != 1 {
			t.Errorf("run %d: %d outputs left alone, want 1 (photo.png's photo.webp)", i, s.left)
		}
		if n := strings.Count(string(read(t, filepath.Join(src, manifestName))), "│ photo.webp"); n != 1 {
			t.Errorf("run %d: manifest lists photo.webp %d times, want once", i, n)
		}
	}
}

// TestManifestKeepsEarlierWork: the manifest describes everything cpwo made,
// so a narrower run (-webp) and a run on one file, the way a save hook calls
// it, must keep the rest. The single-file run also updates the project's
// manifest instead of starting one beside the file.
func TestManifestKeepsEarlierWork(t *testing.T) {
	src := t.TempDir()
	putFile(t, filepath.Join(src, "index.html"), "<!doctype html><html><body>  <p>hi</p>  </body></html>")
	putFile(t, filepath.Join(src, "assets", "style.css"), "body {\n  color: #ffffff;\n}\n")
	writePNG(t, filepath.Join(src, "pic.png"), 64)
	def := allOn()
	delete(def, "avif")

	check := func(when string) {
		t.Helper()
		man := string(read(t, filepath.Join(src, manifestName)))
		for _, want := range []string{"index.min.html", "assets/style.min.css", "pic.webp"} {
			if !strings.Contains(man, want) {
				t.Errorf("%s: manifest lost %q:\n%s", when, want, man)
			}
		}
	}
	if _, err := run([]string{src}, options{quality: 82, jobs: 2, on: def}); err != nil {
		t.Fatal(err)
	}
	check("after a full run")
	if _, err := run([]string{src}, options{quality: 82, jobs: 2, on: map[string]bool{"webp": true}}); err != nil {
		t.Fatal(err)
	}
	check("after a -webp run")
	// Each partial run must also find the project's record, or it would take
	// cpwo's own outputs for somebody else's and leave them stale.
	style := filepath.Join(src, "assets", "style.css")
	for i, c := range []struct{ path, color string }{
		{style, "#123456"}, {filepath.Join(src, "index.html"), ""}, {filepath.Join(src, "assets"), "#234567"},
	} {
		if c.color != "" {
			putFile(t, style, "body {\n  color: "+c.color+";\n}\n")
			later := time.Now().Add(time.Duration(i+2) * time.Second)
			if err := os.Chtimes(style, later, later); err != nil {
				t.Fatal(err)
			}
		}
		s, err := run([]string{c.path}, options{quality: 82, jobs: 2, on: def})
		if err != nil {
			t.Fatal(err)
		}
		check("after a run on " + filepath.Base(c.path))
		if s.left != 0 {
			t.Errorf("a run on %s left %d of cpwo's own files alone", filepath.Base(c.path), s.left)
		}
		if c.color != "" && !strings.Contains(string(read(t, filepath.Join(src, "assets", "style.min.css"))), c.color) {
			t.Errorf("a run on %s did not redo style.min.css", filepath.Base(c.path))
		}
	}
	if exists(filepath.Join(src, "assets", manifestName)) {
		t.Error("a run on one file or subfolder started a second manifest instead of joining the project's")
	}
}

// TestSubfolderRecordsAreFolded: a run on one subfolder, before the project had
// a record, starts one there. The next run on the whole project folds it into
// the project's record, removes it, and still treats those outputs as cpwo's.
func TestSubfolderRecordsAreFolded(t *testing.T) {
	site := filepath.Join(t.TempDir(), "site")
	style := filepath.Join(site, "css", "style.css")
	putFile(t, style, "body {\n  color: #ffffff;\n}\n")
	putFile(t, filepath.Join(site, "index.html"), "<!doctype html><html><body>  <p>hi</p>  </body></html>")
	o := options{quality: 82, jobs: 2, on: map[string]bool{"html": true, "css": true}}

	if _, err := run([]string{filepath.Join(site, "css")}, o); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(site, "css", manifestName)) {
		t.Fatal("a first run on a subfolder kept no record there; the case is not set up")
	}
	if _, err := run([]string{site}, o); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(site, "css", manifestName)) {
		t.Error("the subfolder's record was not folded into the project's")
	}
	if man := string(read(t, filepath.Join(site, manifestName))); !strings.Contains(man, "css/style.min.css") {
		t.Errorf("the project's record lacks css/style.min.css:\n%s", man)
	}

	putFile(t, style, "body {\n  color: #000000;\n}\n")
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(style, later, later); err != nil {
		t.Fatal(err)
	}
	s, err := run([]string{site}, o)
	if err != nil {
		t.Fatal(err)
	}
	if s.left != 0 || !strings.Contains(string(read(t, filepath.Join(site, "css", "style.min.css"))), "#000") {
		t.Errorf("style.min.css was not redone after the fold (left alone: %d)", s.left)
	}
}

// TestOldRecordNeverDeletes: records from before ownershipSince also listed
// files cpwo merely found up to date, such as a hand-made pic.webp. Their
// entries may be overwritten, as those versions did, but one that loses is only
// dropped from the record, never deleted, and is left alone after that.
func TestOldRecordNeverDeletes(t *testing.T) {
	src := t.TempDir()
	writePNG(t, filepath.Join(src, "pic.png"), 64)
	handMade := strings.Repeat("hand-made, bigger than the PNG ", 2000)
	putFile(t, filepath.Join(src, "pic.webp"), handMade) // written last, so up to date
	current := version
	version = "0.2.1-beta"
	putFile(t, filepath.Join(src, manifestName), render([]entry{{src: "pic.png", outs: []string{"pic.webp"}}}))
	version = current

	o := options{quality: 82, jobs: 1, on: map[string]bool{"webp": true}}
	for i := 1; i <= 2; i++ {
		s, err := run([]string{src}, o)
		if err != nil {
			t.Fatal(err)
		}
		if !exists(filepath.Join(src, "pic.webp")) || string(read(t, filepath.Join(src, "pic.webp"))) != handMade {
			t.Fatalf("run %d: the hand-made pic.webp an old record listed was replaced or deleted", i)
		}
		if s.removed != 0 {
			t.Errorf("run %d: removed %d files", i, s.removed)
		}
	}
	if exists(filepath.Join(src, manifestName)) && strings.Contains(string(read(t, filepath.Join(src, manifestName))), "pic.webp") {
		t.Error("the record still claims pic.webp after cpwo gave up on it")
	}
}

// TestFoundStaysUndeletable: an old record's entry stays undeletable for as long
// as cpwo has not written the file itself, across any number of runs, not just
// the first one after the upgrade.
func TestFoundStaysUndeletable(t *testing.T) {
	src := t.TempDir()
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, noisyImage(256), &jpeg.Options{Quality: 3}); err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(jpg.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if wb, _ := bestWebP(decoded, false, 82); len(wb) < jpg.Len() {
		t.Fatal("a WebP of this JPEG wins; the case is not set up")
	}
	grad := filepath.Join(src, "grad.jpg")
	putFile(t, grad, jpg.String())
	handMade := "a hand-made grad.webp, smaller than the JPEG" // so it wins the first run
	putFile(t, filepath.Join(src, "grad.webp"), handMade)
	current := version
	version = "0.2.1-beta"
	putFile(t, filepath.Join(src, manifestName), render([]entry{{src: "grad.jpg", outs: []string{"grad.webp"}}}))
	version = current

	o := options{quality: 82, jobs: 1, on: map[string]bool{"webp": true}}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	if man := string(read(t, filepath.Join(src, manifestName))); !strings.Contains(man, foundPrefix+"grad.webp\n") {
		t.Fatalf("the rewritten record does not keep grad.webp as found:\n%s", man)
	}
	later := time.Now().Add(2 * time.Second) // the source changes, and no WebP of it wins
	if err := os.Chtimes(grad, later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(src, "grad.webp")) || string(read(t, filepath.Join(src, "grad.webp"))) != handMade {
		t.Error("the hand-made grad.webp was deleted or replaced on the second run")
	}
}

// TestCaseVariantIsNotOwned: claims fold case, but on a case-sensitive
// filesystem Photo.webp beside a listed photo.webp is another file, and
// somebody else's.
func TestCaseVariantIsNotOwned(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs a case-sensitive filesystem")
	}
	src := t.TempDir()
	writeJPEG(t, filepath.Join(src, "photo.jpg"), photoImage(96), 92)
	o := options{quality: 82, jobs: 2, on: map[string]bool{"webp": true}}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	writeJPEG(t, filepath.Join(src, "Photo.jpg"), photoImage(96), 92)
	handMade := strings.Repeat("hand-made, bigger than the JPEG ", 3000)
	putFile(t, filepath.Join(src, "Photo.webp"), handMade)
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(src, "Photo.webp")) || string(read(t, filepath.Join(src, "Photo.webp"))) != handMade {
		t.Error("the hand-made Photo.webp was taken for the listed photo.webp")
	}
	if !exists(filepath.Join(src, "photo.webp")) || !strings.Contains(string(read(t, filepath.Join(src, manifestName))), "│ photo.webp") {
		t.Error("photo.webp was lost from disk or from the record")
	}
}

// TestCaseVariantKeepsSiblingListed: Photo.png sorts first and takes the
// case-folded claim, writing Photo.webp. On Linux that is another file from
// photo.png's listed photo.webp, which must stay in the record.
func TestCaseVariantKeepsSiblingListed(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs a case-sensitive filesystem")
	}
	src := t.TempDir()
	writePNG(t, filepath.Join(src, "photo.png"), 64)
	o := options{quality: 82, jobs: 2, on: map[string]bool{"webp": true}}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(src, "Photo.png"), 48)
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	if man := string(read(t, filepath.Join(src, manifestName))); !strings.Contains(man, "│ photo.webp") {
		t.Errorf("photo.webp dropped out of the record:\n%s", man)
	}
}

// TestWriteInPlaceLeavesUnopenableFile: the Windows fallback must not remove a
// file it could not even open; only a write that fails after truncating does.
func TestWriteInPlaceLeavesUnopenableFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file the current user cannot write")
	}
	dst := filepath.Join(t.TempDir(), "style.min.css")
	putFile(t, dst, "a{color:red}")
	if err := os.Chmod(dst, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := writeInPlace(dst, []byte("b{}")); err == nil {
		t.Fatal("wrote over a read-only file")
	}
	if got := string(read(t, dst)); got != "a{color:red}" {
		t.Errorf("the read-only file was changed or removed: %q", got)
	}
}

// TestUndecodableSiblingTakesNothing: an image that will not decode makes
// nothing, so it must not take photo.webp from photo.png beside it.
func TestUndecodableSiblingTakesNothing(t *testing.T) {
	src := t.TempDir()
	writePNG(t, filepath.Join(src, "photo.png"), 64)
	o := options{quality: 82, jobs: 2, on: map[string]bool{"webp": true}}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(src, "photo.jpg"), "not really a JPEG")
	for i := 1; i <= 2; i++ {
		s, err := run([]string{src}, o)
		if err != nil {
			t.Fatal(err)
		}
		if s.left != 0 || !strings.Contains(string(read(t, filepath.Join(src, manifestName))), "│ photo.webp") {
			t.Errorf("run %d: photo.webp was taken from photo.png (left alone: %d)", i, s.left)
		}
	}
}

// TestUnreadableEntryIsKept: a folder that cannot be read just now does not
// mean its outputs are gone. Forgetting them would disown them for good.
func TestUnreadableEntryIsKept(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder the current user cannot read")
	}
	a := t.TempDir()
	putFile(t, filepath.Join(a, "top.css"), "body {\n  color: #ffffff;\n}\n")
	putFile(t, filepath.Join(a, "sub", "style.css"), "p {\n  margin: 0px;\n}\n")
	o := options{quality: 82, jobs: 1, on: map[string]bool{"css": true}}
	if _, err := run([]string{a}, o); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(a, "sub")
	if err := os.Chmod(sub, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sub, 0o755) })
	if _, err := run([]string{filepath.Join(a, "top.css")}, o); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if man := string(read(t, filepath.Join(a, manifestName))); !strings.Contains(man, "sub/style.min.css") {
		t.Errorf("an unreadable folder's output was dropped from the record:\n%s", man)
	}
	if s, _ := run([]string{a}, o); s.left != 0 {
		t.Errorf("%d of cpwo's own outputs were left alone afterwards", s.left)
	}
}

// TestNestedPathsShareOneRecord: given a folder and a subfolder of it, in
// either order, cpwo keeps one record for both and folds in the subfolder's.
func TestNestedPathsShareOneRecord(t *testing.T) {
	for _, order := range [][]string{{"sub", "."}, {".", "sub"}} {
		a := t.TempDir()
		putFile(t, filepath.Join(a, "sub", "old.css"), "body {\n  color: #ffffff;\n}\n")
		o := options{quality: 82, jobs: 2, on: map[string]bool{"css": true}}
		if _, err := run([]string{filepath.Join(a, "sub")}, o); err != nil {
			t.Fatal(err)
		}
		putFile(t, filepath.Join(a, "sub", "new.css"), "p {\n  margin: 0px;\n}\n")
		if _, err := run([]string{filepath.Join(a, order[0]), filepath.Join(a, order[1])}, o); err != nil {
			t.Fatal(err)
		}
		if exists(filepath.Join(a, "sub", manifestName)) {
			t.Errorf("%v: the subfolder still has its own record", order)
		}
		man := string(read(t, filepath.Join(a, manifestName)))
		if !strings.Contains(man, "sub/new.min.css") || !strings.Contains(man, "sub/old.min.css") {
			t.Errorf("%v: the record lost an output:\n%s", order, man)
		}
		if s, _ := run([]string{a}, o); s.left != 0 {
			t.Errorf("%v: %d of cpwo's own outputs were left alone afterwards", order, s.left)
		}
	}
}

// TestEmptyRecordIsRemoved: once nothing of cpwo's is left, its record goes.
// One that still named photo.webp would make a photo.webp someone exports
// later look like cpwo's.
func TestEmptyRecordIsRemoved(t *testing.T) {
	src := t.TempDir()
	photo := filepath.Join(src, "photo.jpg")
	writeJPEG(t, photo, photoImage(256), 92)
	o := options{quality: 82, jobs: 1, on: map[string]bool{"webp": true}}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(src, manifestName)) {
		t.Fatal("no record after the first run; the case is not set up")
	}
	writeJPEG(t, photo, noisyImage(256), 3) // so small that no WebP beats it
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(photo, later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(src, "photo.webp")) || exists(filepath.Join(src, manifestName)) {
		t.Error("photo.webp or the now empty record survived")
	}
}

// TestAdopt: outputs committed without their record come back from a clone
// looking like somebody else's files. cpwo leaves them alone and says so, and
// -adopt takes them over.
func TestAdopt(t *testing.T) {
	src := t.TempDir()
	style := filepath.Join(src, "style.css")
	putFile(t, style, "body {\n  color: #ffffff;\n}\n")
	o := options{quality: 82, jobs: 1, on: map[string]bool{"css": true}}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(src, manifestName)); err != nil {
		t.Fatal(err)
	}
	putFile(t, style, "body {\n  color: #000000;\n}\n")
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(style, later, later); err != nil {
		t.Fatal(err)
	}

	s, err := run([]string{src}, o)
	if err != nil {
		t.Fatal(err)
	}
	if s.left != 1 || !s.hint {
		t.Errorf("without a record: %d left alone, hint %v; want 1 and a hint about -adopt", s.left, s.hint)
	}
	o.adopt = true
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(read(t, filepath.Join(src, "style.min.css"))), "#000") {
		t.Error("-adopt did not take style.min.css over")
	}
	o.adopt = false
	if s, _ := run([]string{src}, o); s.left != 0 || s.hint {
		t.Errorf("after -adopt: %d left alone, hint %v; the record should list it now", s.left, s.hint)
	}
}

// TestRelinkVendorPair: a vendor ships lib.css and lib.min.css together, in
// whatever mtime order the archive left them. The page is pointed at the
// vendor's lib.min.css either way, and the file itself is not touched.
func TestRelinkVendorPair(t *testing.T) {
	src := t.TempDir()
	vendor := ".a{color:red}\n"
	putFile(t, filepath.Join(src, "lib.css"), ".a {\n  color: red;\n}\n")
	putFile(t, filepath.Join(src, "lib.min.css"), vendor)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(src, "lib.min.css"), old, old); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(src, "index.html"),
		`<!doctype html><html><head><link rel="stylesheet" href="lib.css"></head><body>x</body></html>`)

	o := options{quality: 82, jobs: 2, on: map[string]bool{"html": true, "css": true}, relink: true}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	if got := string(read(t, filepath.Join(src, "index.min.html"))); !strings.Contains(got, "href=lib.min.css") {
		t.Errorf("not relinked to the vendor's lib.min.css:\n%s", got)
	}
	if got := string(read(t, filepath.Join(src, "lib.min.css"))); got != vendor {
		t.Errorf("the vendor's lib.min.css was changed to %q", got)
	}
}

// TestVariantGoesWhenSourceNarrows: cpwo never upscales, so once the source is
// no wider than a size, that size's variant shows the old picture and goes.
func TestVariantGoesWhenSourceNarrows(t *testing.T) {
	src := t.TempDir()
	pic := filepath.Join(src, "pic.png")
	writePNG(t, pic, 256)
	o := options{quality: 82, jobs: 1, resize: true, sizes: []int{128}, on: map[string]bool{"webp": true}}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(src, "pic-128.webp")) {
		t.Fatal("no pic-128.webp; the case is not set up")
	}
	writePNG(t, pic, 100)
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(pic, later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := run([]string{src}, o); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(src, "pic-128.webp")) {
		t.Error("pic-128.webp survived although the source is only 100px wide now")
	}
}

// TestReadRecord: a record reads back the way render wrote it, with Windows
// line endings too, and says whether a version from before ownershipSince
// wrote it.
func TestReadRecord(t *testing.T) {
	dir := t.TempDir()
	text := render([]entry{
		{src: "a.png", outs: []string{"a.min.png", "a.webp"}},
		{src: "b.css", outs: []string{"b.min.css"}},
	})
	for _, c := range []struct {
		header string
		legacy bool
	}{{"CPWO v" + version + " ", false}, {"CPWO v0.2.1-beta ", true}, {"CPWO vdev ", true}} {
		body := strings.Replace(text, "CPWO v"+version+" ", c.header, 1)
		putFile(t, filepath.Join(dir, manifestName), strings.ReplaceAll(body, "\n", "\r\n"))
		rec, err := readRecord(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(rec.entries["a.png"], []string{"a.min.png", "a.webp"}) ||
			!slices.Equal(rec.entries["b.css"], []string{"b.min.css"}) || len(rec.entries) != 2 {
			t.Errorf("%q: read back %v", c.header, rec.entries)
		}
		if rec.legacy != c.legacy {
			t.Errorf("%q: legacy = %v, want %v", c.header, rec.legacy, c.legacy)
		}
	}
}

// TestOneBadFileDoesNotStopTheRun: a file that cannot be read is reported and
// counted, and everything else, the HTML wave and the manifest included, still
// happens. An image too wide for WebP is not a failure at all, just no .webp.
func TestOneBadFileDoesNotStopTheRun(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file the current user cannot read")
	}
	src := t.TempDir()
	putFile(t, filepath.Join(src, "a.css"), "body {\n  color: #ffffff;\n}\n")
	bad := filepath.Join(src, "b.css")
	putFile(t, bad, "p {\n  margin: 0px;\n}\n")
	if err := os.Chmod(bad, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(bad, 0o644) })
	putFile(t, filepath.Join(src, "index.html"), "<!doctype html><html><body>  <p>hi</p>  </body></html>")
	wide := image.NewRGBA(image.Rect(0, 0, webpMax+17, 2))
	rng := rand.New(rand.NewSource(1))
	for i := range wide.Pix {
		wide.Pix[i] = uint8(rng.Intn(256))
	}
	writeImage(t, filepath.Join(src, "wide.png"), wide)

	def := allOn()
	delete(def, "avif")
	s, err := run([]string{src}, options{quality: 82, jobs: 2, on: def})
	if err != nil {
		t.Fatalf("a file that failed stopped the run: %v", err)
	}
	if s.failed != 1 {
		t.Errorf("%d failed, want 1 (b.css)", s.failed)
	}
	for _, f := range []string{"a.min.css", "index.min.html", manifestName} {
		if !exists(filepath.Join(src, f)) {
			t.Errorf("%s missing: the failure stopped the run", f)
		}
	}
	if exists(filepath.Join(src, "wide.webp")) {
		t.Error("wrote a WebP wider than the format allows")
	}
}

// goModules lists go.mod's requirements as module, version pairs.
func goModules(t *testing.T) [][2]string {
	t.Helper()
	var out [][2]string
	for _, m := range regexp.MustCompile(`(?m)^\s+(\S+) (v\S+)`).FindAllStringSubmatch(string(read(t, "go.mod")), -1) {
		out = append(out, [2]string{m[1], m[2]})
	}
	return out
}

// TestLicenseNotices: licenses.md carries the full text of everything a release
// build links. scripts/deps.sh licenses writes it; this fails when a dependency
// moves on without it.
func TestLicenseNotices(t *testing.T) {
	if !strings.Contains(licenseText, "MIT License") || !strings.Contains(licenseText, "CyClone") {
		t.Errorf("LICENSE is not cpwo's MIT license:\n%s", licenseText)
	}
	notices := strings.ReplaceAll(string(read(t, "licenses.md")), "\r\n", "\n") // a Windows checkout may add CRs
	for _, want := range []string{"lib/LICENSE.libwebp", "lib/LICENSE.libavif", "lib/LICENSE.aom", "Apache License",
		"## libyuv\n", "The LibYuv Project Authors"} {
		if !strings.Contains(notices, want) {
			t.Errorf("licenses.md lacks %q", want)
		}
	}
	for _, m := range goModules(t) {
		if m[0] == "github.com/ebitengine/purego" {
			continue // only linked without -tags nodynamic, so never shipped
		}
		if !strings.Contains(notices, "## "+m[0]+" "+m[1]+"\n") {
			t.Errorf("licenses.md has no section for %s %s: run scripts/deps.sh licenses", m[0], m[1])
		}
	}
}

// TestLicenseList: -license prints cpwo's own license first, then one line per
// component of go.mod, then where the full texts are.
func TestLicenseList(t *testing.T) {
	var buf bytes.Buffer
	printLicense(&buf)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if first := lines[0]; !strings.HasPrefix(first, "CPWO "+version) || !strings.Contains(first, "MIT") ||
		!strings.Contains(first, "Copyright (c) 2026 CyClone") {
		t.Errorf("first line is not cpwo's own license: %q", first)
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "licenses.md") {
		t.Errorf("last line does not point at licenses.md: %q", last)
	}
	// three aligned columns: every row's license starts where cpwo's does
	col := strings.Index(lines[0], "MIT")
	for i, c := range builtWith {
		if line := lines[i+2]; strings.Index(line, c[1]) != col {
			t.Errorf("license column out of line: %q", line)
		}
	}
	for _, m := range goModules(t) {
		name := strings.TrimPrefix(m[0], "github.com/")
		found := false
		for _, c := range builtWith {
			found = found || strings.HasPrefix(c[0], name)
		}
		if !found {
			t.Errorf("-license does not list %s; add it to builtWith", m[0])
		}
	}
}

func replaceMin(rel string) string {
	dir, base := filepath.Split(rel)
	ext := filepath.Ext(base)
	return filepath.Join(dir, base[:len(base)-len(ext)-len(".min")]+ext)
}

func putFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// noisyImage makes a photo-like test image. A smooth gradient would be the
// wrong fixture: PNG beats WebP on those, and cpwo correctly declines to write
// a .webp that is bigger than its source.
func noisyImage(w int) image.Image {
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, w))
	for y := 0; y < w; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255})
		}
	}
	return img
}

// photoImage is the other kind of fixture: smooth shapes plus grain, the way a
// photograph looks to an encoder. Pure static (noisyImage) compresses badly in
// every format and would tell us nothing about which one wins.
func photoImage(w int) image.Image {
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, w))
	for y := 0; y < w; y++ {
		for x := 0; x < w; x++ {
			v := (x*7 + y*3) / 5 % 200
			img.Set(x, y, color.RGBA{
				uint8(v + rng.Intn(40)),
				uint8(v/2 + rng.Intn(30)),
				uint8(200 - v/3),
				255,
			})
		}
	}
	return img
}

func writePNG(t *testing.T, path string, w int) {
	t.Helper()
	writeImage(t, path, noisyImage(w))
}

func writeImage(t *testing.T, path string, img image.Image) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	putFile(t, path, buf.String())
}

func writeJPEG(t *testing.T, path string, img image.Image, quality int) {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
	putFile(t, path, buf.String())
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func size(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
