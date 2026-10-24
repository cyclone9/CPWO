# Changelog

CPWO uses semantic versioning: the third number for fixes, the second for features that keep the structure, the first for a breaking change; a `-beta` suffix marks the versions before the first complete release, 1.0.0.

## Unreleased

## 1.0.0 - 2026-10-24

- The READMEs show the CPWO logo and a short presentation video; the logo, the cover images and the videos are not covered by the MIT License.
- Release builds use `-tags nodynamic`, so the Linux binary is fully static and no binary loads a system libwebp or libavif.
- `-picture` leaves an `<img>` alone when it has a `srcset` or an image path in a `data-` attribute.
- `.cpwo-output` is now the record of what CPWO owns: CPWO replaces or removes only files it lists, and leaves vendor files and hand-made ones alone even when they sit where an output would go.
- Outdated outputs are removed: a sidecar that no longer wins on size, one left over from a picture that was replaced, and a resized variant from when the source was wider.
- There is one `.cpwo-output` per project: a run on a single file or a subfolder updates it, records that older runs left in subfolders are folded into it, and a record with nothing left in it is removed.
- Entries in records written by 0.2.1-beta and earlier are never deleted. They keep a `# Kept as found, never deleted:` header line until CPWO writes the file itself.
- New `-adopt` flag: takes over files already sitting where CPWO writes, for when the record was lost or never committed.
- When two sources map to one output (`photo.jpg` and `photo.png` to `photo.webp`), the first in name order gets it on every run.
- A file that fails no longer stops the run: it is reported and the run ends with exit status 1. All paths are checked before work starts, and flags may come after paths.
- Every file is written to a temporary file and renamed into place, so an interrupted run never leaves half a file.
- Images wider or taller than 16383 px get no `.webp`, and CPWO prints a warning.
- New `-license` flag: prints CPWO's license and the license of each component it is built with. The full texts are in `licenses.md`, which ships with every release.
- `licenses.md` also carries the license of libyuv, which the AVIF encoder compiles in without shipping one.
- Release archives include `CHANGELOG.md`.
- `scripts/build.sh` builds all six targets into `bin/<os>-<arch>/` with checksums, and `scripts/deps.sh` checks every dependency for updates and regenerates `licenses.md`.
- On GitHub, every push and pull request runs the tests on Linux, Windows and macOS and uploads the six binaries. A release runs the tests first.
- The README is also available in Persian, Turkish and Arabic.
