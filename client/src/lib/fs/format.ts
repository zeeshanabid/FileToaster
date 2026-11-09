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
