// Shared archive-format helpers for the file-manager window. `archiveFormat` is
// the client's one source of truth for the "supported" invariant: it returns the
// same mapping as the backend's `decompressFormat` (see
// internal/fs/ops/decompress.go), so the Decompress toolbar button and the
// backend backstop agree on which files an archive is.
//
// Extension → format, matched on the lower-cased base name, precedence matters:
//   1. `.tar.gz` / `.tgz` → `targz`
//   2. `.zip` → `zip`
//   3. `.tar` → `tar`
//   4. anything else (incl. standalone `.gz`, `.bz2`, `.xz`, `.zst`, `.rar`,
//      `.7z`, no-ext) → `` (not an archive)

// ArchiveFormat is the client-side classification of a file name, or "" when the
// name is not a supported archive.
export type ArchiveFormat = 'zip' | 'targz' | 'tar' | '';

// archiveFormat returns the archive format a file name denotes, or "" when it is
// not a supported archive. Matching is case-insensitive and follows the
// precedence order above.
export function archiveFormat(name: string): ArchiveFormat {
  const lower = name.toLowerCase();
  if (lower.endsWith('.tar.gz')) return 'targz';
  if (lower.endsWith('.tgz')) return 'targz';
  if (lower.endsWith('.zip')) return 'zip';
  if (lower.endsWith('.tar')) return 'tar';
  return '';
}

// isArchiveFile reports whether name denotes a supported archive. The Decompress
// button uses it to enable itself only for archive files.
export function isArchiveFile(name: string): boolean {
  return archiveFormat(name) !== '';
}

// archiveStem returns name without its archive extension — `myfiles.zip` →
// `myfiles`, `myfiles.tar.gz` → `myfiles` — the name the "Extract in …" folder
// is derived from. A name with no archive extension is returned unchanged.
export function archiveStem(name: string): string {
  if (name.endsWith('.tar.gz')) return name.slice(0, -'.tar.gz'.length);
  if (name.endsWith('.tgz')) return name.slice(0, -'.tgz'.length);
  if (name.endsWith('.zip')) return name.slice(0, -'.zip'.length);
  if (name.endsWith('.tar')) return name.slice(0, -'.tar'.length);
  return name;
}
