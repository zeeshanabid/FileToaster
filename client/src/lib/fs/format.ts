// Shared formatting helpers for the file-manager window and the information
// dialog, kept in one module so both stay consistent.

const SIZE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];

// formatSize renders a byte count in human-readable form, e.g. 3 → "3 B",
// 1536 → "2 KB", 5242880 → "5 MB". Whole-unit values round to an integer;
// a value that lands below its smallest shown unit keeps one decimal.
export function formatSize(n: number): string {
  if (n < 1024) return `${n} B`;
  let v = n;
  let i = 0;
  while (v >= 1024 && i < SIZE_UNITS.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 1 ? 1 : 0)} ${SIZE_UNITS[i]}`;
}

// formatDate renders an ISO-8601 timestamp as a local, user-friendly date/time
// string, or an empty string when the value is not a valid date.
export function formatDate(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  });
}

const RATE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB'];

// formatRate renders a bytes-per-second throughput in a human-friendly form,
// e.g. 512 → "512 B/s", 2048 → "2 KB/s", 5_368_709 → "5 MB/s". It is used to
// show a task's live transfer speed.
export function formatRate(n: number): string {
  if (!isFinite(n) || n < 0) return '0 B/s';
  if (n < 1024) return `${Math.round(n)} B/s`;
  let v = n;
  let i = 0;
  while (v >= 1024 && i < RATE_UNITS.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 1 ? 1 : 0)} ${RATE_UNITS[i]}/s`;
}

// getFileType returns the file type in uppercase from a filename.
// For directories, it returns "DIR". For files, it returns the extension
// in uppercase (e.g., "JPG", "TXT", "PDF"). If no extension, returns "FILE".
export function getFileType(name: string, type: 'file' | 'dir'): string {
  if (type === 'dir') return 'DIR';
  const idx = name.lastIndexOf('.');
  if (idx === -1 || idx === name.length - 1) return 'FILE';
  return name.slice(idx + 1).toUpperCase();
}

// --- Search helpers --------------------------------------------------------

// DatePreset is the advanced search's relative date filter. The relative keys
// map to a concrete [after, before] range at search time; 'all' means no bound
// and 'custom' uses the picker's explicit from/to values.
export type DatePreset = '1h' | '6h' | '1d' | '7d' | '30d' | '1y' | 'all' | 'custom';

// DATE_PRESETS is the ordered list the advanced dialog renders.
export const DATE_PRESETS: DatePreset[] = ['all', '1h', '6h', '1d', '7d', '30d', '1y', 'custom'];

// formatDatePreset renders a preset key as a human label.
export function formatDatePreset(preset: DatePreset): string {
  switch (preset) {
    case '1h':
      return 'Last 1 hour';
    case '6h':
      return 'Last 6 hours';
    case '1d':
      return 'Last 24 hours';
    case '7d':
      return 'Last 7 days';
    case '30d':
      return 'Last 30 days';
    case '1y':
      return 'Last year';
    case 'custom':
      return 'Custom range';
    default:
      return 'All time';
  }
}

const PRESET_MS: Record<Exclude<DatePreset, 'all' | 'custom'>, number> = {
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '1d': 24 * 60 * 60 * 1000,
  '7d': 7 * 24 * 60 * 60 * 1000,
  '30d': 30 * 24 * 60 * 60 * 1000,
  '1y': 365 * 24 * 60 * 60 * 1000
};

// presetToDateRange converts a preset into an ISO [after, before] range anchored
// at `now` (injectable for tests). 'all' and 'custom' return empty bounds; a
// relative preset bounds `before` at now and `after` at now minus its span.
export function presetToDateRange(
  preset: DatePreset,
  now: Date = new Date()
): { after: string; before: string } {
  const span = PRESET_MS[preset as Exclude<DatePreset, 'all' | 'custom'>];
  if (!span) return { after: '', before: '' };
  return {
    after: new Date(now.getTime() - span).toISOString(),
    before: now.toISOString()
  };
}

// SizeUnit is the unit selector used by the advanced dialog's size fields.
export type SizeUnit = 'B' | 'KB' | 'MB' | 'GB';

export const SEARCH_SIZE_UNITS: SizeUnit[] = ['B', 'KB', 'MB', 'GB'];

const UNIT_BYTES: Record<SizeUnit, number> = { B: 1, KB: 1024, MB: 1024 ** 2, GB: 1024 ** 3 };

// sizeToBytes converts a value + unit into a byte count (rounded). A non-finite
// or negative value yields 0 (no bound).
export function sizeToBytes(value: number, unit: SizeUnit): number {
  if (!isFinite(value) || value <= 0) return 0;
  return Math.round(value * UNIT_BYTES[unit]);
}

// normalizeExtension trims whitespace, strips any leading dot(s), and
// lower-cases an extension entry (".TXT" and "txt" both become "txt").
export function normalizeExtension(raw: string): string {
  return raw.trim().replace(/^\.+/, '').toLowerCase();
}

// commonMimeTypes maps file extensions to MIME types for the advanced dialog's
// fast client-side detection. The server is the source of truth for filtering;
// this is only used to show a type hint on a result without a round trip.
const commonMimeTypes: Record<string, string> = {
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  png: 'image/png',
  gif: 'image/gif',
  webp: 'image/webp',
  bmp: 'image/bmp',
  svg: 'image/svg+xml',
  tif: 'image/tiff',
  tiff: 'image/tiff',
  ico: 'image/x-icon',
  heic: 'image/heic',
  mp4: 'video/mp4',
  mov: 'video/quicktime',
  avi: 'video/x-msvideo',
  mkv: 'video/x-matroska',
  webm: 'video/webm',
  mp3: 'audio/mpeg',
  wav: 'audio/wav',
  flac: 'audio/flac',
  ogg: 'audio/ogg',
  m4a: 'audio/mp4',
  aac: 'audio/aac',
  pdf: 'application/pdf',
  doc: 'application/msword',
  docx: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  xls: 'application/vnd.ms-excel',
  xlsx: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
  ppt: 'application/vnd.ms-powerpoint',
  pptx: 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
  zip: 'application/zip',
  tar: 'application/x-tar',
  gz: 'application/gzip',
  tgz: 'application/gzip',
  txt: 'text/plain',
  md: 'text/markdown',
  csv: 'text/csv',
  html: 'text/html',
  htm: 'text/html',
  css: 'text/css',
  js: 'text/javascript',
  mjs: 'text/javascript',
  json: 'application/json',
  xml: 'application/xml',
  yaml: 'application/yaml',
  yml: 'application/yaml',
  go: 'text/x-go',
  ts: 'text/x-typescript',
  py: 'text/x-python',
  rs: 'text/x-rust',
  java: 'text/x-java',
  sh: 'application/x-sh'
};

// mimeTypeForFilename returns the MIME type for a file's extension, or an empty
// string when the extension is unknown.
export function mimeTypeForFilename(name: string): string {
  const idx = name.lastIndexOf('.');
  if (idx === -1 || idx === name.length - 1) return '';
  return commonMimeTypes[name.slice(idx + 1).toLowerCase()] ?? '';
}

// MimeOption is one entry in the advanced dialog's file-type multi-select. The
// value is what the server matches against (exact or "prefix/*").
export interface MimeOption {
  value: string;
  label: string;
  group: string;
}

// MIME_TYPE_OPTIONS is the catalogue of file-type filters the advanced dialog
// offers, grouped for display. Custom MIME types can still be typed in.
export const MIME_TYPE_OPTIONS: MimeOption[] = [
  { value: 'image/*', label: 'Images', group: 'Media' },
  { value: 'video/*', label: 'Videos', group: 'Media' },
  { value: 'audio/*', label: 'Audio', group: 'Media' },
  { value: 'application/pdf', label: 'PDF', group: 'Documents' },
  { value: 'application/msword', label: 'Word (doc)', group: 'Documents' },
  {
    value: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
    label: 'Word (docx)',
    group: 'Documents'
  },
  { value: 'application/vnd.ms-excel', label: 'Excel (xls)', group: 'Documents' },
  {
    value: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
    label: 'Excel (xlsx)',
    group: 'Documents'
  },
  { value: 'application/zip', label: 'ZIP', group: 'Archives' },
  { value: 'application/x-tar', label: 'TAR', group: 'Archives' },
  { value: 'application/gzip', label: 'GZIP', group: 'Archives' },
  { value: 'text/plain', label: 'Text', group: 'Text' },
  { value: 'text/*', label: 'Any text', group: 'Text' },
  { value: 'text/x-*', label: 'Code', group: 'Text' },
  { value: 'text/markdown', label: 'Markdown', group: 'Text' }
];
