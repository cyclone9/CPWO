package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// manifestName is the record cpwo keeps of what it put in a project: every
// generated file is listed in it and nothing else is. It is read back at the
// start of every run, because it is also how cpwo knows which files it may
// replace or remove.
const manifestName = ".cpwo-output"

// ownershipSince is the first version whose records list only files cpwo
// wrote or kept as its own. Older ones also listed whatever sat up to date at
// an output path, a hand-made photo.webp included, so their entries are
// trusted for overwriting, as those versions did, but never for deleting.
const ownershipSince = "0.3.0"

// foundPrefix starts a header line naming an output that is cpwo's to update
// but not to delete: an old record's entry, or a file taken over with
// -adopt, that cpwo has not written itself since. The line goes once cpwo
// writes the file.
const foundPrefix = "# Kept as found, never deleted: "

// entry is one source file and everything cpwo produced from it, all paths
// relative to the scanned root and slash-separated.
type entry struct {
	src     string
	outs    []string
	in, out int64
}

// manifest collects what each source produced, grouped by the root that
// records it. The sets are keyed by claimKey.
type manifest struct {
	mu       sync.Mutex
	byRoot   map[string][]entry
	wrote    map[string]bool // outputs this run wrote
	adopted  map[string]bool // outputs -adopt took over
	disowned map[string]bool // outputs given up on but left on disk
}

func newManifest() *manifest {
	return &manifest{
		byRoot:   make(map[string][]entry),
		wrote:    make(map[string]bool),
		adopted:  make(map[string]bool),
		disowned: make(map[string]bool),
	}
}

func (m *manifest) add(r *result) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, set := range []struct {
		to    map[string]bool
		paths []string
	}{{m.wrote, r.wrote}, {m.adopted, r.adopted}, {m.disowned, r.disowned}} {
		for _, p := range set.paths {
			set.to[claimKey(p)] = true
		}
	}
	if len(r.outs) == 0 {
		return
	}
	root := r.j.root
	e := entry{src: relSlash(root, r.j.src)}
	for _, o := range r.outs {
		e.outs = append(e.outs, relSlash(root, o))
	}
	m.byRoot[root] = append(m.byRoot[root], e)
}

// relSlash is p relative to root, with forward slashes. root is absolute.
func relSlash(root, p string) string {
	if rel, err := filepath.Rel(root, absPath(p)); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

// absPath is p made absolute and clean.
func absPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// claimKey is the form ownership and claims are kept in. It folds case, so
// photo.webp and Photo.webp count as one file the way they do on Windows and
// macOS.
func claimKey(p string) string { return strings.ToLower(absPath(p)) }

func onDisk(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// sameAsAny reports whether p is one of paths, by name or, where names differ
// only in case, by being the same file on disk.
func sameAsAny(p string, paths []string) bool {
	for _, q := range paths {
		if q == p {
			return true
		}
		a, err := os.Stat(p)
		if err != nil {
			return false
		}
		if b, err := os.Stat(q); err == nil && os.SameFile(a, b) {
			return true
		}
	}
	return false
}

// anchor is the directory whose .cpwo-output records what cpwo makes from p:
// the nearest one at or above p, so a run on the file you just saved, or on
// one subfolder, updates the project's record instead of starting another.
// The search stops at the top of a git checkout. With no record found, a
// folder starts its own, and a file starts one in its folder.
func anchor(p string) string {
	start := absPath(p)
	if fi, err := os.Stat(start); err != nil || !fi.IsDir() {
		start = filepath.Dir(start)
	}
	for d := start; ; d = filepath.Dir(d) {
		if onDisk(filepath.Join(d, manifestName)) {
			return d
		}
		if onDisk(filepath.Join(d, ".git")) || filepath.Dir(d) == d {
			return start
		}
	}
}

// state is what every worker shares for one run: which files on disk cpwo
// made, and which source each possible output belongs to. The maps are
// complete before the first worker starts and only read after that.
type state struct {
	owned     map[string]string              // claimKey -> the exact path a record lists
	uncertain map[string]bool                // claimKey of listed outputs cpwo may update but not delete
	claims    map[string]string              // claimKey(output) -> the source that may write it
	prev      map[string]map[string][]string // root -> what its records listed, root-relative
	folded    map[string][]string            // root -> subfolder records folded into its own
	recorded  bool                           // some record existed before this run
}

// owns reports whether a record lists the file at p as cpwo's. The lookup
// folds case, the way Windows and macOS compare names, but then insists that
// p and the listed path are one file on disk: on Linux, a Photo.webp next to a
// listed photo.webp is somebody else's.
func (st *state) owns(p string) bool {
	listed, ok := st.owned[claimKey(p)]
	if !ok {
		return false
	}
	if listed == absPath(p) {
		return true
	}
	a, err := os.Stat(listed)
	if err != nil {
		return false
	}
	b, err := os.Stat(p)
	return err == nil && os.SameFile(a, b)
}

// newState reads the record at each root, and the ones runs on single
// subfolders left below it (subs), and hands out the claims.
func newState(roots []string, subs map[string][]string, jobs []job, o options) (*state, error) {
	st := &state{
		owned:     make(map[string]string),
		uncertain: make(map[string]bool),
		claims:    make(map[string]string),
		prev:      make(map[string]map[string][]string),
		folded:    make(map[string][]string),
	}
	for _, root := range roots {
		st.prev[root] = make(map[string][]string)
		for _, dir := range append([]string{root}, subs[root]...) {
			rec, err := readRecord(dir)
			if err != nil {
				if dir == root {
					return nil, err
				}
				// what it lists stays unowned, so is left alone: the safe side
				warn("%v", err)
				continue
			}
			if rec.entries == nil {
				continue
			}
			st.recorded = true
			if dir != root {
				st.folded[root] = append(st.folded[root], filepath.Join(dir, manifestName))
			}
			for src, outs := range rec.entries {
				s := relSlash(root, filepath.Join(dir, filepath.FromSlash(src)))
				for _, out := range outs {
					abs := filepath.Join(dir, filepath.FromSlash(out))
					st.prev[root][s] = append(st.prev[root][s], relSlash(root, abs))
					st.owned[claimKey(abs)] = abs
					if rec.legacy || rec.found[out] {
						st.uncertain[claimKey(abs)] = true
					}
				}
			}
		}
	}
	// Walk order decides who gets a contested output, so the outcome is the
	// same on every run: photo-512.jpg sorts before photo.jpg and keeps
	// photo-512.webp from becoming photo.jpg's 512px variant. An image that
	// will not decode makes nothing, so it must not keep a name from a sibling
	// that will.
	for _, j := range jobs {
		if j.group == "img" && !decodable(j.src) {
			continue
		}
		for _, dst := range outputs(j, o) {
			if k := claimKey(dst); st.claims[k] == "" {
				st.claims[k] = j.src
			}
		}
	}
	return st, nil
}

// record is one .cpwo-output as read back: source -> outputs, relative to its
// directory and slash-separated; the outputs it keeps as found; and whether a
// version from before ownershipSince wrote it.
type record struct {
	entries map[string][]string
	found   map[string]bool
	legacy  bool
}

// readRecord reads the .cpwo-output in dir. With no file there, entries is nil.
func readRecord(dir string) (record, error) {
	b, err := os.ReadFile(filepath.Join(dir, manifestName))
	if errors.Is(err, fs.ErrNotExist) {
		return record{}, nil
	}
	if err != nil {
		return record{}, err
	}
	rec := record{entries: make(map[string][]string), found: make(map[string]bool), legacy: true}
	inMap, src := false, ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r") // a git checkout may have added CRs
		switch {
		case strings.HasPrefix(line, "# Written by CPWO v"):
			v, _, _ := strings.Cut(strings.TrimPrefix(line, "# Written by CPWO v"), " ")
			rec.legacy = olderThan(v, ownershipSince)
		case !inMap && strings.HasPrefix(line, foundPrefix):
			rec.found[strings.TrimPrefix(line, foundPrefix)] = true
		case strings.HasPrefix(line, "# File Map"):
			inMap = true
		case strings.HasPrefix(line, "# File Tree"):
			return rec, nil
		case inMap && strings.HasPrefix(line, "│ ") && strings.HasSuffix(line, " │"):
			cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "│ "), " │"), " │ ")
			if len(cells) != 2 {
				continue
			}
			s, out := strings.TrimRight(cells[0], " "), strings.TrimRight(cells[1], " ")
			if s == "Source" && out == "Generated" {
				continue // the header row
			}
			if s != "" {
				src = s // a blank source cell continues the row above
			}
			if src != "" && out != "" {
				rec.entries[src] = append(rec.entries[src], out)
			}
		}
	}
	return rec, nil
}

// olderThan compares the numeric part of two versions, so 0.2.1-beta is older
// than 0.3.0 and 0.3.0-beta is not. A version it cannot read counts as old.
func olderThan(v, than string) bool {
	a, b := versionParts(v), versionParts(than)
	if a == nil {
		return true
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func versionParts(v string) []int {
	v, _, _ = strings.Cut(v, "-") // a pre-release or git describe suffix
	f := strings.Split(v, ".")
	if len(f) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, s := range f {
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

// writeAll writes the record at each root. roots is passed in so the order
// matches the command line rather than map iteration order. Each record is
// what was there before plus what this run did, less anything no longer on
// disk: a run that only did -webp, or only one file, must not make cpwo forget
// the rest of its work. A record left with nothing in it is removed, since one
// that still named those paths would make a file someone puts there later look
// like cpwo's. Subfolder records folded into a root's record are removed too.
func (m *manifest) writeAll(roots []string, st *state) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, root := range roots {
		outs := make(map[string]map[string]bool) // source -> its outputs
		add := func(src, out string) {
			if outs[src] == nil {
				outs[src] = make(map[string]bool)
			}
			outs[src][out] = true
		}
		recorded := make(map[string][]string) // claimKey -> exact paths some source recorded this run
		for _, e := range m.byRoot[root] {
			for _, out := range e.outs {
				add(e.src, out)
				abs := filepath.Join(root, filepath.FromSlash(out))
				recorded[claimKey(abs)] = append(recorded[claimKey(abs)], abs)
			}
		}
		for src, list := range st.prev[root] {
			for _, out := range list {
				abs := filepath.Join(root, filepath.FromSlash(out))
				k := claimKey(abs)
				if m.disowned[k] {
					continue
				}
				// An output this run handed to another source has changed hands
				// once that source recorded that very file. Until then the old
				// entry stands: an undecodable photo.jpg must not orphan
				// photo.png's photo.webp, and on Linux a Photo.webp written for
				// Photo.png is not photo.png's photo.webp.
				if owner, claimed := st.claims[k]; claimed && relSlash(root, owner) != src && sameAsAny(abs, recorded[k]) {
					continue
				}
				add(src, out)
			}
		}

		var entries []entry
		var found []string
		for src, set := range outs {
			e := entry{src: src}
			for out := range set {
				abs := filepath.Join(root, filepath.FromSlash(out))
				fi, err := os.Stat(abs)
				if errors.Is(err, fs.ErrNotExist) {
					continue // removed, by this run or by hand
				}
				// Any other error (a folder that cannot be read just now) keeps
				// the entry: forgetting it would disown the file for good.
				e.outs = append(e.outs, out)
				if err == nil {
					e.out += fi.Size()
				}
				if k := claimKey(abs); (st.uncertain[k] || m.adopted[k]) && !m.wrote[k] {
					found = append(found, out)
				}
			}
			if len(e.outs) == 0 {
				continue
			}
			sort.Strings(e.outs)
			if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(src))); err == nil {
				e.in = fi.Size()
			}
			entries = append(entries, e)
		}

		file := filepath.Join(root, manifestName)
		if len(entries) == 0 {
			if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		} else {
			sort.Slice(entries, func(i, j int) bool { return entries[i].src < entries[j].src })
			sort.Strings(found)
			if err := writeFile(file, []byte(render(entries, found...))); err != nil {
				return err
			}
		}
		for _, f := range st.folded[root] {
			if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// render draws a record. found are the outputs it keeps as found (foundPrefix),
// one header line each; a project that never ran an older cpwo or -adopt has
// none.
func render(entries []entry, found ...string) string {
	var b strings.Builder
	var all []string
	var in, out int64
	for _, e := range entries {
		all = append(all, e.outs...)
		in += e.in
		out += e.out
	}
	saved := 0.0
	if in > 0 {
		saved = 100 * float64(in-out) / float64(in)
	}

	fmt.Fprintf(&b, "# Written by CPWO v%s on %s\n", version, time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "# Result: %d sources -> %d generated files, %s -> %s (%.1f%% smaller)\n",
		len(entries), len(all), human(in), human(out), saved)
	for _, f := range found {
		b.WriteString(foundPrefix + f + "\n")
	}
	b.WriteString("\n")

	b.WriteString("# File Map\n\n")
	writeTable(&b, entries)

	b.WriteString("\n# File Tree\n\n")
	sort.Strings(all)
	writeTree(&b, all)
	return b.String()
}

// writeTable draws the source-to-output map as a box-drawn table. A source with
// several outputs (a .min.png and a .webp, say) gets one row per output, the
// left cell blank after the first.
func writeTable(b *strings.Builder, entries []entry) {
	left, right := len("Source"), len("Generated")
	for _, e := range entries {
		left = max(left, width(e.src))
		for _, o := range e.outs {
			right = max(right, width(o))
		}
	}

	rule := func(l, m, r string) {
		fmt.Fprintf(b, "%s%s%s%s%s\n", l, strings.Repeat("─", left+2), m, strings.Repeat("─", right+2), r)
	}
	row := func(a, c string) {
		fmt.Fprintf(b, "│ %s%s │ %s%s │\n",
			a, strings.Repeat(" ", left-width(a)), c, strings.Repeat(" ", right-width(c)))
	}

	rule("┌", "┬", "┐")
	row("Source", "Generated")
	rule("├", "┼", "┤")
	for _, e := range entries {
		for i, o := range e.outs {
			if i == 0 {
				row(e.src, o)
			} else {
				row("", o)
			}
		}
	}
	rule("└", "┴", "┘")
}

// width counts runes, not bytes, so a non-ASCII filename still lines up.
func width(s string) int { return utf8.RuneCountInString(s) }

// node is one level of the generated-file tree.
type node struct {
	children map[string]*node
	isFile   bool
}

func newNode() *node { return &node{children: make(map[string]*node)} }

func (n *node) insert(parts []string) {
	if len(parts) == 0 {
		n.isFile = true
		return
	}
	c, ok := n.children[parts[0]]
	if !ok {
		c = newNode()
		n.children[parts[0]] = c
	}
	c.insert(parts[1:])
}

func writeTree(b *strings.Builder, paths []string) {
	root := newNode()
	for _, p := range paths {
		root.insert(strings.Split(path.Clean(p), "/"))
	}
	b.WriteString(".\n")
	root.render(b, "")
}

func (n *node) render(b *strings.Builder, prefix string) {
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	// directories first, then files, each alphabetically
	sort.Slice(names, func(i, j int) bool {
		ci, cj := n.children[names[i]], n.children[names[j]]
		if di, dj := len(ci.children) > 0, len(cj.children) > 0; di != dj {
			return di
		}
		return names[i] < names[j]
	})

	for i, name := range names {
		last := i == len(names)-1
		branch, indent := "├── ", "│   "
		if last {
			branch, indent = "└── ", "    "
		}
		fmt.Fprintf(b, "%s%s%s\n", prefix, branch, name)
		n.children[name].render(b, prefix+indent)
	}
}
