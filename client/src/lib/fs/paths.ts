// Path helpers shared by the folder tree, window navigation, and the windows
// store. They exist because naive `root + '/'` comparisons break when the
// configured root is the filesystem root: `'/' + '/'` is `'//'`, which matches
// no absolute path, so every containment check and chain build below `/` fails
// silently (clicks in the tree stop opening folders and deep jumps no-op).

// normalizePath trims redundant trailing slashes, keeping "/" itself intact.
function normalizePath(p: string): string {
  if (p === '') return p;
  const trimmed = p.replace(/\/+$/, '');
  return trimmed === '' ? '/' : trimmed;
}

// isWithin reports whether `path` is `root` itself or nested under it. Both
// sides are normalized so "/" and a trailing slash on the root are handled.
export function isWithin(path: string, root: string): boolean {
  const target = normalizePath(path);
  const base = normalizePath(root);
  if (target === base) return true;
  if (base === '/') return target.startsWith('/');
  return target.startsWith(base + '/');
}

// relativeUnder returns `path` relative to `root` (empty when equal), or null
// when `path` is not inside `root`. The filesystem root yields no leading slash
// so the result splits cleanly into path segments.
export function relativeUnder(path: string, root: string): string | null {
  const target = normalizePath(path);
  const base = normalizePath(root);
  if (target === base) return '';
  if (base === '/') {
    return target.startsWith('/') ? target.replace(/^\/+/, '') : null;
  }
  return target.startsWith(base + '/') ? target.slice(base.length + 1) : null;
}

// joinPath appends a child name to a directory without doubling the separator.
export function joinPath(dir: string, name: string): string {
  return dir.endsWith('/') ? dir + name : dir + '/' + name;
}

// basename returns the final path segment. It keeps the filesystem root as "/"
// and, unlike `p.slice(p.lastIndexOf('/') + 1)` guarded by `i <= 0`, does not
// return the whole path for a direct child of "/" (e.g. "/data").
export function basename(p: string): string {
  if (p === '/') return p;
  const trimmed = normalizePath(p);
  const i = trimmed.lastIndexOf('/');
  return i < 0 ? trimmed : trimmed.slice(i + 1);
}
