// CPWO — CyClone Power Web Optimizer.
// Scans a project directory, minifies HTML/CSS/JS/JSON/SVG/XML, recompresses
// JPEG/PNG and emits WebP and (opt-in) AVIF, optionally at several widths. Every result is
// written next to its source; the sources themselves are never modified, and
// everything produced is listed in a .cpwo-output manifest at the root.
package main

import (
	"bytes"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/avif"
	"github.com/gen2brain/webp"
	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/html"
	"github.com/tdewolff/minify/v2/js"
	"github.com/tdewolff/minify/v2/json"
	"github.com/tdewolff/minify/v2/svg"
	"github.com/tdewolff/minify/v2/xml"
	xdraw "golang.org/x/image/draw"
)

// version is the tag form goreleaser injects ({{.Version}}, so no leading v),
// kept in sync by hand so a local build reports what a release would.
var version = "1.0.0" // overridden by goreleaser ldflags

// licenseText is cpwo's own license; -license prints its copyright line.
//
//go:embed LICENSE
var licenseText string

// builtWith is what -license lists after cpwo's own license: each component,
// its license and what it does in cpwo, one license per row. A row indented
// by two spaces is a C library compiled into the encoder above it. The full
// texts are in licenses.md, which scripts/deps.sh licenses writes and every
// release ships. Keep this in step with go.mod, the Dependencies table in the
// READMEs and the library versions scripts/deps.sh reports; TestLicenseList
// fails when a module is missing.
var builtWith = [][3]string{
	{"tdewolff/minify/v2", "MIT", "HTML, CSS, JS, JSON, SVG and XML minification"},
	{"gen2brain/webp", "MIT", "WebP encoding"},
	{"  libwebp 1.6.0", "BSD-3-Clause", "compiled into gen2brain/webp"},
	{"gen2brain/avif", "MIT", "AVIF encoding"},
	{"  libavif 1.4.2", "BSD-2-Clause", "compiled into gen2brain/avif"},
	{"  aom 3.14.1", "BSD-2-Clause", "compiled into gen2brain/avif"},
	{"  dav1d 1.5.3", "BSD-2-Clause", "compiled into gen2brain/avif"},
	{"  libyuv", "BSD-3-Clause", "compiled into gen2brain/avif"},
	{"golang.org/x/net/html", "BSD-3-Clause", "HTML parsing for -relink and -picture"},
	{"golang.org/x/image/draw", "BSD-3-Clause", "Downscaling for -r"},
	{"tdewolff/parse/v2", "MIT", "Parsers behind the minifiers"},
	{"tetratelabs/wazero", "Apache-2.0", "Runs the AVIF encoder's WebAssembly"},
	{"golang.org/x/sys", "BSD-3-Clause", "CPU detection and system calls"},
	{"Go", "BSD-3-Clause", "Standard library and runtime"},
	{"ebitengine/purego", "Apache-2.0", "Not in release builds, only without -tags nodynamic"},
	{"goreleaser", "MIT", "Builds the releases, not shipped"},
}

// printLicense writes cpwo's own license first, then builtWith, in three
// aligned columns, then where the full texts are.
func printLicense(w io.Writer) {
	copyright := "MIT License"
	for _, line := range strings.Split(licenseText, "\n") {
		if strings.HasPrefix(line, "Copyright") {
			copyright = strings.TrimSpace(line)
			break
		}
	}
	rows := append([][3]string{{"CPWO " + version, "MIT", copyright}}, builtWith...)
	name, lic := 0, 0
	for _, r := range rows {
		name, lic = max(name, len(r[0])), max(lic, len(r[1]))
	}
	for i, r := range rows {
		fmt.Fprintln(w, strings.TrimRight(fmt.Sprintf("%-*s    %-*s    %s", name, r[0], lic, r[1], r[2]), " "))
		if i == 0 {
			fmt.Fprintln(w)
		}
	}
	fmt.Fprintln(w, "\nThe full license texts are in licenses.md, which ships with every release.")
}

// groups are the switchable units of work. Naming any of them on the command
// line restricts the run to just those; otherwise every group in defaultOn runs.
var groups = []string{"html", "css", "js", "json", "svg", "xml", "img", "webp", "avif"}

// defaultOff lists groups that stay quiet unless asked for. AVIF beats WebP on
// size but its encoder is an order of magnitude slower, which is the wrong
// default for a tool people wire into a save hook.
var defaultOff = map[string]bool{"avif": true}

// modern are the sidecar formats written beside each JPEG/PNG, in the order a
// <picture> should offer them: best compression first.
var modern = []struct {
	group, ext, mime string
	encode           func(img image.Image, isPNG bool, quality int) ([]byte, error)
	count            func(*result)
}{
	{"avif", ".avif", "image/avif", bestAVIF, func(r *result) { r.avifs++ }},
	{"webp", ".webp", "image/webp", bestWebP, func(r *result) { r.webps++ }},
}

// webpMax is the widest and tallest a WebP image can be.
const webpMax = 16383

var extGroup = map[string]string{
	".html": "html", ".htm": "html",
	".css": "css",
	".js":  "js", ".mjs": "js",
	".json": "json",
	".svg":  "svg",
	".xml":  "xml",
	".jpg":  "img", ".jpeg": "img", ".png": "img",
}

var groupMedia = map[string]string{
	"html": "text/html",
	"css":  "text/css",
	"js":   "application/javascript",
	"json": "application/json",
	"svg":  "image/svg+xml",
	"xml":  "text/xml",
}

type options struct {
	quality        int
	sizes          []int
	resize         bool
	resizeFallback bool
	exif           bool
	relink         bool
	picture        bool
	overwrite      bool
	adopt          bool
	jobs           int
	verbose        bool
	on             map[string]bool
}

type stats struct {
	scanned, written, skipped, in, out, webps, avifs, variants int64
	removed, left, unlisted, failed                            int64
	// hint: files were left alone for not being listed, and there was no
	// record to list them, which is what a lost or uncommitted record looks like
	hint bool
}

const usage = `cpwo %s — CyClone Power Web Optimizer

usage: cpwo [flags] <project-path>...

Scans each path and writes optimized files NEXT TO the originals:
  style.css -> style.min.css      photo.jpg -> photo.webp
  app.js    -> app.min.js         photo.jpg -> photo-512.webp   (with -r)
Your source files are never modified. Everything cpwo generates is listed in a
.cpwo-output file at the root of each scanned path, with a tree of the results.
cpwo only ever replaces or removes a file listed there: anything else that sits
where an output would go, a vendor's bootstrap.min.css say, is left alone.
Flags may come before or after the paths.

what to run (all on by default except -avif; naming any runs ONLY those):
  -html -css -js -json -svg -xml    minify these
  -img                              recompress JPEG/PNG at -q
  -webp                             emit .webp beside each JPEG/PNG
  -avif                             emit .avif too: smaller still, but the
                                    encoder is ~10x slower, so it is opt-in.
                                    Off by default, so it ADDS to a normal run
                                    instead of narrowing it to itself
  -only                             run nothing but the groups named, which is
                                    the only way to get -avif on its own
  e.g.  cpwo -webp ./site           only WebP conversion, nothing else
        cpwo -css -js ./site        only CSS and JS
        cpwo -avif ./site           the usual run, with AVIF as well
        cpwo -avif -only ./site     AVIF and nothing else
        cpwo -js=false ./site       everything except JS

options:
  -q <1-100>       quality for JPEG and WebP; AVIF is scaled from it
                                                              (default 82)
  -r               also emit resized variants, in each format on (default off)
  -sizes <list>    widths for -r, comma separated; implies -r (default 1024,512,256)
                   never upscales: a 400px image skips 512 and 1024
  -resize-fallback also emit resized JPEG/PNG next to the resized variants,
                   for <img srcset> fallbacks; implies -r
  -relink          point <link href> and <script src> in the minified HTML at
                   the .min. files, when those exist and are up to date
  -picture         wrap <img src="x.jpg"> in <picture> with AVIF and WebP
                   <source>s, keeping every attribute of the original <img>.
                   An <img> with a srcset, or with an image in a data-
                   attribute for a lazy-loader, is left as it is
  -exif            keep the source EXIF in the re-encoded .min.jpg instead of
                   dropping it. Orientation is reset, because cpwo turns the
                   pixels themselves. WebP, AVIF and resized copies never carry
                   it: their encoders cannot write it
  -overwrite       redo cpwo's outputs even when they are already up to date
  -adopt           take over files already sitting where cpwo writes, as if
                   .cpwo-output listed them: for when it was lost or never
                   committed. Vendor files there are taken too, so check -v
  -j <n>           parallel workers                           (default: CPU count)
  -v               also print a line per file, and why one was left alone
  -version         print version and exit
  -license         print cpwo's license (MIT) and the license of each component
                   it is built with, and exit. Full texts: licenses.md
  -help, -h        print this and exit

exit status: 0 done, 1 some files could not be processed, 2 bad usage
`

func main() {
	c, err := parseArgs(os.Args[1:], os.Stderr)
	if errors.Is(err, errUsage) {
		os.Exit(2) // flag has already said what was wrong, with the usage
	}
	if err != nil {
		fail(err)
	}
	switch {
	case c.version:
		fmt.Printf("CPWO v%s\n", version)
		return
	case c.help:
		fmt.Printf(usage, version) // asked for: stdout, exit 0
		return
	case c.license:
		printLicense(os.Stdout)
		return
	}
	if len(c.paths) == 0 {
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}

	s, err := run(c.paths, c.o)
	if err == nil || s.scanned > 0 {
		report(s)
	}
	if s.hint {
		warn("%d files sit where cpwo writes, but no %s lists them, so they were left alone (-v names them). "+
			"If an earlier cpwo run made them, run once with -adopt; leave alone any you or a vendor made.",
			s.unlisted, manifestName)
	}
	if err != nil {
		fail(err)
	}
	if s.failed > 0 {
		os.Exit(1)
	}
}

// errUsage means the flag package has already told the user what was wrong.
var errUsage = errors.New("bad usage")

// cli is everything the command line asked for.
type cli struct {
	o                      options
	paths                  []string
	version, help, license bool
}

func parseArgs(args []string, stderr io.Writer) (cli, error) {
	var c cli
	fs := flag.NewFlagSet("cpwo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintf(stderr, usage, version) }
	vals := make(map[string]*bool, len(groups))
	for _, g := range groups {
		vals[g] = fs.Bool(g, !defaultOff[g], "")
	}
	fs.IntVar(&c.o.quality, "q", 82, "")
	fs.BoolVar(&c.o.resize, "r", false, "")
	sizes := fs.String("sizes", "1024,512,256", "")
	fs.BoolVar(&c.o.resizeFallback, "resize-fallback", false, "")
	fs.BoolVar(&c.o.exif, "exif", false, "")
	fs.BoolVar(&c.o.relink, "relink", false, "")
	fs.BoolVar(&c.o.picture, "picture", false, "")
	only := fs.Bool("only", false, "")
	fs.BoolVar(&c.o.overwrite, "overwrite", false, "")
	fs.BoolVar(&c.o.adopt, "adopt", false, "")
	fs.IntVar(&c.o.jobs, "j", runtime.NumCPU(), "")
	fs.BoolVar(&c.o.verbose, "v", false, "")
	fs.BoolVar(&c.version, "version", false, "")
	fs.BoolVar(&c.license, "license", false, "")
	fs.BoolVar(&c.help, "help", false, "")
	fs.BoolVar(&c.help, "h", false, "")

	// flag stops at the first argument that is not a flag. Take that one as a
	// path and go on parsing, so `cpwo ./site -avif` means what it says rather
	// than looking for a folder called -avif. After a bare --, everything is a
	// path.
	for {
		if err := fs.Parse(args); err != nil {
			return c, errUsage
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		if n := len(args) - len(rest); n > 0 && args[n-1] == "--" {
			c.paths = append(c.paths, rest...)
			break
		}
		c.paths = append(c.paths, rest[0])
		args = rest[1:]
	}
	if c.version || c.help || c.license {
		return c, nil
	}

	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	c.o.on = resolveGroups(set, vals, *only)
	if len(c.o.on) == 0 {
		return c, fmt.Errorf("-only needs at least one group to run, e.g. cpwo -avif -only")
	}
	if set["sizes"] || c.o.resizeFallback {
		c.o.resize = true
	}
	var err error
	if c.o.sizes, err = parseSizes(*sizes); err != nil {
		return c, err
	}
	if c.o.quality < 1 || c.o.quality > 100 {
		return c, fmt.Errorf("-q must be between 1 and 100")
	}
	if c.o.jobs < 1 {
		c.o.jobs = 1
	}
	return c, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "cpwo:", err)
	os.Exit(2)
}

// warn reports a problem with one file. The run carries on without it.
func warn(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "cpwo: "+format+"\n", a...)
}

// resolveGroups turns the -html/-css/... flags into an enabled set. Asking for a
// group that is on by default (cpwo -webp) restricts the run to the named ones;
// otherwise every group runs except the ones explicitly switched off
// (cpwo -js=false).
//
// A group from defaultOff cannot restrict on its own, because naming it is the
// only way to turn it on at all: if -avif restricted like -webp does, `cpwo
// -avif` would mean "AVIF and nothing else" and there would be no way to say
// "the usual, plus AVIF" short of listing all nine groups. So -avif alone adds
// to the defaults, and -only is how you ask for it by itself.
func resolveGroups(set map[string]bool, vals map[string]*bool, strict bool) map[string]bool {
	only := make(map[string]bool)
	restrict := strict
	for _, g := range groups {
		if !set[g] || !*vals[g] {
			continue
		}
		only[g] = true
		restrict = restrict || !defaultOff[g]
	}
	if restrict {
		return only
	}
	on := make(map[string]bool, len(groups))
	for _, g := range groups {
		on[g] = *vals[g]
	}
	return on
}

// parseSizes reads -sizes into distinct widths, widest first. A width named
// twice would be decided twice, and the second time round the file the first
// one wrote would look like somebody else's.
func parseSizes(s string) ([]int, error) {
	var out []int
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("bad -sizes value %q: want positive integers like 1024,512,256", f)
		}
		out = append(out, n)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return slices.Compact(out), nil
}

func report(s stats) {
	saved := 0.0
	if s.in > 0 {
		saved = 100 * float64(s.in-s.out) / float64(s.in)
	}
	extra := ""
	if s.webps > 0 {
		extra += fmt.Sprintf("  +%d webp", s.webps)
	}
	if s.avifs > 0 {
		extra += fmt.Sprintf("  +%d avif", s.avifs)
	}
	if s.variants > 0 {
		extra += fmt.Sprintf("  +%d resized", s.variants)
	}
	if s.skipped > 0 {
		extra += fmt.Sprintf("  %d up to date", s.skipped)
	}
	if s.removed > 0 {
		extra += fmt.Sprintf("  %d outdated removed", s.removed)
	}
	if s.left > 0 {
		extra += fmt.Sprintf("  %d left alone (-v lists them)", s.left)
	}
	if s.failed > 0 {
		extra += fmt.Sprintf("  %d failed", s.failed)
	}
	fmt.Printf("%d scanned, %d written  %s -> %s  (%.1f%% smaller)%s\n",
		s.scanned, s.written, human(s.in), human(s.out), saved, extra)
}

// job is one source file plus where its outputs go. base is the output path
// without an extension, so every product (.min.css, .webp, -512.webp) is a
// suffix away. root is the directory whose .cpwo-output records the file.
type job struct {
	src, base, ext, group, root string
}

// variantName and fallbackName are where -r and -resize-fallback put a width.
// The .min in the fallback keeps it out of the next scan, so a re-run cannot
// turn photo-512.jpg into photo-512-256.jpg.
func variantName(base string, w int, ext string) string {
	return fmt.Sprintf("%s-%d%s", base, w, ext)
}

func fallbackName(base string, w int, ext string) string {
	return fmt.Sprintf("%s-%d.min%s", base, w, ext)
}

// outputs lists every file cpwo could write for j under o, whether or not this
// run ends up writing it. Each one is handed to a single source before any
// work starts.
func outputs(j job, o options) []string {
	if j.group != "img" {
		return []string{j.base + ".min" + j.ext}
	}
	var out []string
	if o.on["img"] {
		out = append(out, j.base+".min"+j.ext)
	}
	for _, f := range modern {
		if !o.on[f.group] {
			continue
		}
		out = append(out, j.base+f.ext)
		if o.resize {
			for _, w := range o.sizes {
				out = append(out, variantName(j.base, w, f.ext))
			}
		}
	}
	if o.resizeFallback {
		for _, w := range o.sizes {
			out = append(out, fallbackName(j.base, w, j.ext))
		}
	}
	return out
}

func run(paths []string, o options) (stats, error) {
	var s stats
	jobs, roots, subs, err := collect(paths, o, &s)
	if err != nil {
		return s, err
	}
	st, err := newState(roots, subs, jobs, o)
	if err != nil {
		return s, err
	}
	// Two waves. Everything else goes first because the HTML wave looks on disk
	// for the .min.css and .webp files it is about to point at.
	man := newManifest()
	for _, htmlWave := range []bool{false, true} {
		var batch []job
		for _, j := range jobs {
			if (j.group == "html") == htmlWave {
				batch = append(batch, j)
			}
		}
		wave(batch, o, st, &s, man)
	}
	s.hint = s.unlisted > 0 && !st.recorded
	return s, man.writeAll(roots, st)
}

// collect walks every path and returns the files cpwo has something to do
// with, each once and in walk order, plus the directories whose .cpwo-output
// records them, and for each of those, the records runs on single subfolders
// left below it. Every path is checked before any work starts, so a typo in
// the last one cannot leave the first half done.
func collect(paths []string, o options, s *stats) ([]job, []string, map[string][]string, error) {
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			return nil, nil, nil, err
		}
	}
	// A path inside a folder given on the same command line records into that
	// folder's root, so the folder's walk meets the inner record and folds it
	// in, instead of the two roots writing and removing each other's.
	rootOf := make([]string, len(paths))
	for i, p := range paths {
		rootOf[i] = anchor(p)
	}
	for i := range paths {
		for j, q := range paths {
			if fi, err := os.Stat(q); i == j || err != nil || !fi.IsDir() {
				continue
			}
			if within(rootOf[i], absPath(q)) && len(rootOf[j]) < len(rootOf[i]) {
				rootOf[i] = rootOf[j]
			}
		}
	}

	var jobs []job
	var roots []string
	subs := make(map[string][]string)
	seen := make(map[string]bool)
	for i, p := range paths {
		root := rootOf[i]
		if !slices.Contains(roots, root) {
			roots = append(roots, root)
		}
		err := filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				if path == p {
					return err
				}
				// an unreadable folder costs only itself
				warn("%v", err)
				s.failed++
				return nil
			}
			name := d.Name()
			if d.IsDir() {
				// never descend into VCS dirs or dependency dumps, but an
				// explicitly named root is always walked
				if path != p && (name == "node_modules" || name == "vendor" ||
					(len(name) > 1 && name[0] == '.')) {
					return filepath.SkipDir
				}
				return nil
			}
			if name == manifestName {
				// left by a run on just this subfolder: it gets folded in
				if dir := filepath.Dir(absPath(path)); dir != root && !seen[dir] {
					seen[dir] = true
					subs[root] = append(subs[root], dir)
				}
				return nil
			}

			ext := strings.ToLower(filepath.Ext(path))
			group, known := extGroup[ext]
			// bootstrap.min.css is already minified, and cpwo's own output must
			// never be fed back in or a re-run makes style.min.min.css
			if !known || isGenerated(name) ||
				!(o.on[group] || (group == "img" && (o.on["webp"] || o.on["avif"]))) {
				return nil
			}
			// overlapping paths (cpwo site site/assets) must not hand the same
			// file to two workers at once
			abs := absPath(path)
			if seen[abs] {
				return nil
			}
			seen[abs] = true

			jobs = append(jobs, job{
				src:   path,
				base:  strings.TrimSuffix(path, filepath.Ext(path)),
				ext:   ext,
				group: group,
				root:  root,
			})
			return nil
		})
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return jobs, roots, subs, nil
}

func wave(batch []job, o options, st *state, s *stats, man *manifest) {
	ch := make(chan job)
	var wg sync.WaitGroup
	for i := 0; i < o.jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := newMinifier() // one per worker: the parsers are not shareable
			for j := range ch {
				atomic.AddInt64(&s.scanned, 1)
				r, err := process(m, j, o, st)
				if err != nil {
					// One file that cannot be done costs only itself. The rest of
					// the project and its manifest still get written, and the exit
					// status tells a script that something went wrong.
					warn("%s: %v", j.src, err)
					atomic.AddInt64(&s.failed, 1)
				}
				// whatever it did write exists now, error or not
				man.add(&r)
				atomic.AddInt64(&s.written, r.written)
				atomic.AddInt64(&s.skipped, r.skipped)
				if r.written > 0 {
					// a file nothing was written for must not skew the totals
					atomic.AddInt64(&s.in, r.in)
				}
				atomic.AddInt64(&s.out, r.out)
				atomic.AddInt64(&s.webps, r.webps)
				atomic.AddInt64(&s.avifs, r.avifs)
				atomic.AddInt64(&s.variants, r.variants)
				atomic.AddInt64(&s.removed, r.removed)
				atomic.AddInt64(&s.left, r.left)
				atomic.AddInt64(&s.unlisted, r.unlisted)
				if o.verbose && r.written > 0 {
					fmt.Printf("  %s  %s -> %s\n", j.src, human(r.in), human(r.out))
				}
			}
		}()
	}
	for _, j := range batch {
		ch <- j
	}
	close(ch)
	wg.Wait()
}

// within reports whether path p is dir or lies under it.
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// decodable reports whether the image at p has a header Go can read, which is
// what decides whether it will produce anything at all.
func decodable(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	_, _, err = image.DecodeConfig(f)
	return err == nil
}

// isGenerated spots cpwo's own output so a second run ignores it.
func isGenerated(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".webp") || strings.HasSuffix(lower, ".avif") ||
		strings.Contains(lower, ".min.")
}

func newMinifier() *minify.M {
	m := minify.New()
	// KeepDefaultAttrVals is not optional. Dropping type="text" from an <input>
	// is valid HTML, but it silently kills every input[type="text"] rule in a
	// stylesheet and every querySelector that matches on it.
	m.Add("text/html", &html.Minifier{KeepDefaultAttrVals: true})
	m.AddFunc("text/css", css.Minify)
	m.AddFunc("application/javascript", js.Minify)
	m.AddFunc("application/json", json.Minify)
	m.AddFunc("image/svg+xml", svg.Minify)
	m.AddFunc("text/xml", xml.Minify)
	return m
}

// result is what one source file produced. outs lists every output that now
// exists because of it — including ones this run skipped as up to date, so the
// manifest describes the whole output set and not just the latest delta.
type result struct {
	j                                        job
	st                                       *state
	overwrite, adopt, verbose                bool
	in, out                                  int64
	written, skipped, webps, avifs, variants int64
	removed, left, unlisted                  int64
	outs                                     []string // everything that now exists because of the source
	wrote                                    []string // ...of which this run wrote these
	adopted                                  []string // taken over with -adopt
	disowned                                 []string // given up on but left on disk, see drop
}

func process(m *minify.M, j job, o options, st *state) (result, error) {
	r := result{j: j, st: st, overwrite: o.overwrite, adopt: o.adopt, verbose: o.verbose}
	b, err := os.ReadFile(j.src)
	if err != nil {
		return r, err
	}
	r.in = int64(len(b))

	if j.group == "html" && (o.relink || o.picture) {
		// The output of a rewrite depends on which sibling files exist, not just
		// on this file's mtime, so an up-to-date check cannot tell whether it is
		// still correct. Always redo it; HTML files are few and small.
		r.overwrite = true
		// Rewrite before minifying so the minifier normalizes whatever we emit.
		// A rewrite failure is not worth losing the file over.
		if nb, err := rewriteHTML(b, filepath.Dir(j.src), o.relink, o.picture, st.owns); err == nil {
			b = nb
		}
	}
	mt, ok := groupMedia[j.group]
	if !ok {
		return r, processImage(&r, j, b, o)
	}
	dst := j.base + ".min" + j.ext
	if r.fresh(dst) {
		r.skip(dst)
		return r, nil
	}
	var buf bytes.Buffer
	if err := m.Minify(mt, &buf, bytes.NewReader(b)); err != nil {
		// Syntax the minifier rejects: keep the file rather than lose it.
		buf.Reset()
		buf.Write(b)
	}
	_, err = r.put(dst, smaller(buf.Bytes(), b))
	return r, err
}

func processImage(r *result, j job, b []byte, o options) error {
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil // corrupt or an unsupported variant: leave it alone
	}

	// image.Decode ignores the EXIF orientation tag, and none of the encoders
	// write one back, so a photo shot in portrait would come out of cpwo on its
	// side while the untouched original still displayed upright. Turn the pixels
	// once, here, and every output below inherits it.
	app1 := jpegExif(b)
	img = orient(img, exifOrientation(app1))
	isPNG := j.ext == ".png"

	if o.on["img"] {
		dst := j.base + ".min" + j.ext
		if r.fresh(dst) {
			r.skip(dst)
		} else if r.mine(dst) {
			nb, err := encodeSame(img, isPNG, o.quality)
			if err != nil {
				return err
			}
			if !isPNG && o.exif && app1 != nil {
				nb = withExif(nb, exifUpright(app1))
			}
			// Re-encoding an already-optimized image usually grows it, and a twin
			// bigger than the original earns nothing, so write nothing at all,
			// and take away the one an earlier version of the source left.
			if len(nb) < len(b) {
				if _, err := r.put(dst, nb); err != nil {
					return err
				}
			} else if err := r.drop(dst); err != nil {
				return err
			}
		}
	}

	// Sidecars are decided least-preferred first so that each format has to beat
	// everything a browser would otherwise settle for. A .webp heavier than its
	// PNG is dead weight, and an .avif heavier than that .webp is worse than
	// dead weight, because <picture> offers the .avif first. Flat-colour logos
	// hit both cases regularly. Files from earlier runs are held to the same
	// rule: one that loses now is removed, not kept for being up to date.
	floor := int64(len(b))
	for i := len(modern) - 1; i >= 0; i-- {
		f := modern[i]
		if !o.on[f.group] {
			continue
		}
		dst := j.base + f.ext
		if bounds := img.Bounds(); f.group == "webp" && (bounds.Dx() > webpMax || bounds.Dy() > webpMax) {
			warn("%s: no %s, WebP images stop at %d px", j.src, filepath.Base(dst), webpMax)
			continue
		}
		if !r.mine(dst) {
			// Somebody else's file is in the way. While it is up to date a page
			// would still be offered it, so it still sets the bar for the next
			// format.
			if upToDate(j.src, dst) {
				floor = min(floor, fileSize(dst))
			}
			continue
		}
		if r.fresh(dst) {
			if size := fileSize(dst); size < floor {
				r.skip(dst)
				floor = size
			} else if err := r.drop(dst); err != nil {
				return err
			}
			continue
		}
		nb, err := f.encode(img, isPNG, o.quality)
		if err != nil {
			return err
		}
		if int64(len(nb)) >= floor {
			if err := r.drop(dst); err != nil {
				return err
			}
			continue
		}
		wrote, err := r.put(dst, nb)
		if err != nil {
			return err
		}
		if wrote {
			f.count(r)
			floor = int64(len(nb))
		}
	}

	if !o.resize {
		return nil
	}
	for _, w := range o.sizes {
		small := downscale(img, w)
		for _, f := range modern {
			if !o.on[f.group] {
				continue
			}
			if err := r.variant(variantName(j.base, w, f.ext), small, func() ([]byte, error) {
				return f.encode(small, isPNG, o.quality)
			}); err != nil {
				return err
			}
		}
		if !o.resizeFallback {
			continue
		}
		if err := r.variant(fallbackName(j.base, w, j.ext), small, func() ([]byte, error) {
			return encodeSame(small, isPNG, o.quality)
		}); err != nil {
			return err
		}
	}
	return nil
}

// variant writes one resized output unless it is already up to date. small is
// nil when the source is no wider than this size: cpwo never upscales, and a
// variant left from when the source was wider shows the old picture, so it
// goes.
func (r *result) variant(dst string, small image.Image, encode func() ([]byte, error)) error {
	if small == nil {
		return r.drop(dst)
	}
	if r.fresh(dst) {
		r.skip(dst)
		return nil
	}
	if !r.mine(dst) {
		return nil
	}
	b, err := encode()
	if err != nil {
		return err
	}
	wrote, err := r.put(dst, b)
	if wrote {
		r.variants++
	}
	return err
}

// fresh reports whether dst exists and is no older than the source, so there
// is nothing to redo unless -overwrite says otherwise.
func (r *result) fresh(dst string) bool {
	return !r.overwrite && upToDate(r.j.src, dst)
}

// mine reports whether this source may write, keep or remove dst. Two things
// have to hold. No other source in the run claimed it first: photo.jpg and
// photo.png both want photo.webp, and the first in walk order gets it. And a
// file already sitting there must be one cpwo made, which is what .cpwo-output
// records. Anything else, a vendor's bootstrap.min.css or a .webp someone
// exported by hand, is somebody's own file, and cpwo keeps its hands off it,
// unless -adopt says the record was lost and these are cpwo's after all.
func (r *result) mine(dst string) bool {
	why := ""
	if owner := r.st.claims[claimKey(dst)]; owner != r.j.src {
		why = owner + " makes it too"
	} else if onDisk(dst) && !r.st.owns(dst) {
		if !r.adopt {
			why = "not listed in " + manifestName + ", so not cpwo's"
			r.unlisted++
		} else {
			r.adopted = append(r.adopted, dst)
		}
	}
	if why == "" {
		return true
	}
	r.left++
	if r.verbose {
		fmt.Printf("  %s  left alone: %s\n", dst, why)
	}
	return false
}

// put writes dst unless it belongs to somebody else, and reports whether it did.
func (r *result) put(dst string, b []byte) (bool, error) {
	if !r.mine(dst) {
		return false, nil
	}
	if err := writeFile(dst, b); err != nil {
		return false, err
	}
	r.written++
	r.out += int64(len(b))
	r.outs = append(r.outs, dst)
	r.wrote = append(r.wrote, dst)
	return true, nil
}

// skip records an output that already exists and is current. It counts towards
// the manifest but not towards this run's byte totals.
func (r *result) skip(dst string) {
	if !r.mine(dst) {
		return
	}
	r.skipped++
	r.outs = append(r.outs, dst)
}

// drop removes an earlier output of this source that is no longer worth
// keeping: it lost to the source or to a better format, or the source changed
// under it. Only a file .cpwo-output lists as cpwo's goes; anything else stays.
func (r *result) drop(dst string) error {
	k := claimKey(dst)
	if !onDisk(dst) || r.st.claims[k] != r.j.src || !r.st.owns(dst) {
		return nil
	}
	if r.st.uncertain[k] {
		// Kept as found: an old record listed it, or -adopt took it, and cpwo
		// has not written it since, so it may be somebody's own file. Stop
		// claiming it, but leave it where it is.
		r.disowned = append(r.disowned, dst)
		return nil
	}
	if err := os.Remove(dst); err != nil {
		return err
	}
	r.removed++
	if r.verbose {
		fmt.Printf("  %s  removed: outdated\n", dst)
	}
	return nil
}

// encodeSame re-encodes img in the format it came from, for -img and
// -resize-fallback.
func encodeSame(img image.Image, isPNG bool, quality int) ([]byte, error) {
	var buf bytes.Buffer
	var err error
	if isPNG {
		err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img)
	} else {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	}
	return buf.Bytes(), err
}

// bestWebP encodes img, also trying lossless for PNG sources: flat colour and
// alpha routinely come out several times smaller that way.
func bestWebP(img image.Image, isPNG bool, quality int) ([]byte, error) {
	var lossy bytes.Buffer
	if err := webp.Encode(&lossy, img, webp.Options{Quality: quality}); err != nil {
		return nil, err
	}
	if !isPNG {
		return lossy.Bytes(), nil
	}
	// ponytail: encodes every PNG twice. Fine for web-sized assets; gate it
	// behind a flag if cpwo is ever pointed at a folder of huge PNGs.
	var lossless bytes.Buffer
	if err := webp.Encode(&lossless, img, webp.Options{Lossless: true}); err != nil {
		return nil, err
	}
	return smaller(lossy.Bytes(), lossless.Bytes()), nil
}

// avifSpeed is libavif's speed knob, 0 slowest to 10 fastest. The encoder runs
// as WASM here, so anything below 8 costs more seconds than it saves bytes.
const avifSpeed = 8

// bestAVIF encodes img as AVIF. There is no lossy/lossless race like WebP's:
// AVIF lossless is both far slower and routinely larger than the source PNG, so
// the size guard in processImage would throw it away anyway.
func bestAVIF(img image.Image, _ bool, quality int) ([]byte, error) {
	var buf bytes.Buffer
	// AVIF's quality scale is not JPEG's: q60 already looks like a JPEG q82, so
	// -q is scaled down rather than handing AVIF a number that wastes bytes.
	err := avif.Encode(&buf, img, avif.Options{
		Quality:      avifQuality(quality),
		QualityAlpha: quality,
		Speed:        avifSpeed,
	})
	return buf.Bytes(), err
}

// avifQuality maps a JPEG/WebP quality onto the roughly equivalent AVIF one.
// ponytail: a straight line through (82,60) pinned at 100; good enough for the
// 70-95 range anyone actually passes. Swap in a real curve if it ever matters.
func avifQuality(q int) int {
	v := int(math.Round(float64(q) * 60 / 82))
	if q >= 100 {
		return 100
	}
	if v < 1 {
		return 1
	}
	return v
}

// upToDate reports whether dst already exists and is no older than src, i.e.
// there is nothing new to do unless -overwrite says otherwise.
func upToDate(src, dst string) bool {
	di, err := os.Stat(dst)
	if err != nil {
		return false
	}
	si, err := os.Stat(src)
	if err != nil {
		return false
	}
	return !di.ModTime().Before(si.ModTime())
}

func fileSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// writeFile replaces dst in one step: the bytes go to a temporary file beside
// it, which is then renamed over dst. An interrupted run leaves the old file or
// the new one, never half of one, and half of one would look up to date to
// every run after it.
func writeFile(dst string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".cpwo-*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	if err == nil {
		err = rename(tmp.Name(), dst)
	}
	if err == nil {
		return nil
	}
	os.Remove(tmp.Name())
	if runtime.GOOS == "windows" {
		// ponytail: Windows will not rename over a file another program holds
		// open (a web server, a virus scanner), so write in place, which is not
		// atomic. Queue the file for the end of the run if that ever bites.
		return writeInPlace(dst, b)
	}
	return err
}

// writeInPlace overwrites dst directly. If it cannot even open dst (read-only,
// say), dst is left exactly as it was. Only a write that fails after the open
// has truncated dst removes it, since half a file would look up to date to
// every later run.
func writeInPlace(dst string, b []byte) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
	}
	return err
}

// rename retries on Windows, where a virus scanner looking at the new file for
// a moment is enough to make the first attempt fail.
func rename(from, to string) error {
	err := os.Rename(from, to)
	for i := 0; err != nil && runtime.GOOS == "windows" && i < 5; i++ {
		time.Sleep(100 * time.Millisecond)
		err = os.Rename(from, to)
	}
	return err
}

// downscale returns img resized to width w, or nil if that would be an upscale.
func downscale(img image.Image, w int) image.Image {
	b := img.Bounds()
	if b.Dx() <= w {
		return nil
	}
	h := int(math.Round(float64(b.Dy()) * float64(w) / float64(b.Dx())))
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dst
}

func smaller(a, b []byte) []byte {
	if len(b) < len(a) {
		return b
	}
	return a
}

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
