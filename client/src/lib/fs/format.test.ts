import { describe, it, expect } from 'vitest';
import { formatSize, formatDate, formatRate } from '$lib/fs/format';

describe('formatSize', () => {
	// Each pair is [input bytes, expected human string]. The on-disk values are
	// what POST /api/fs/info returns for /root/go (9670656) and a 5 MB file.
	const cases: Array<[number, string]> = [
		[0, '0 B'],
		[1, '1 B'],
		[999, '999 B'],
		[1023, '1023 B'],
		[1024, '1 KB'],
		[1500, '1 KB'],
		[1536, '2 KB'],
		[1048576, '1 MB'],
		[1048577, '1 MB'],
		[8 * 1024 * 1024, '8 MB'],
		[5242880, '5 MB'],
		[9670656, '9 MB'],
		[1073741824, '1 GB'],
		[3 * 1073741824, '3 GB'],
		[1099511627776, '1 TB'],
		[1125899906842624, '1 PB']
	];

	it.each(cases)('formats %s bytes as %s', (bytes, want) => {
		expect(formatSize(bytes)).toBe(want);
	});

	it('stops at PB and never reports a value below its unit', () => {
		// 1024^6 = 1024 PB: the unit caps at PB even though the magnitude is larger.
		expect(formatSize(1024 ** 6)).toBe('1024 PB');
	});

	it('rounds sub-unit bytes to the nearest whole value in that unit', () => {
		expect(formatSize(1024 * 1.4)).toBe('1 KB');
		expect(formatSize(1024 * 1.6)).toBe('2 KB');
	});
});

describe('formatDate', () => {
	it('renders a valid ISO timestamp as a local string', () => {
		const out = formatDate('2023-01-02T03:04:05Z');
		expect(out).toBeTruthy();
		// Rendered in the host locale, but the year is always present.
		expect(out).toContain('2023');
	});

	it('returns an empty string for values that are not valid dates', () => {
		expect(formatDate('')).toBe('');
		expect(formatDate('not-a-date')).toBe('');
		expect(formatDate('2023-13-40')).toBe('');
	});
});

describe('formatRate', () => {
	const cases: Array<[number, string]> = [
		[0, '0 B/s'],
		[512, '512 B/s'],
		[1023, '1023 B/s'],
		[1024, '1 KB/s'],
		[1536, '2 KB/s'],
		[1048576, '1 MB/s'],
		[5 * 1024 * 1024, '5 MB/s'],
		[3 * 1073741824, '3 GB/s']
	];
	it.each(cases)('formats %s bytes/s as %s', (rate, want) => {
		expect(formatRate(rate)).toBe(want);
	});

	it('treats non-finite and negative rates as zero', () => {
		expect(formatRate(NaN)).toBe('0 B/s');
		expect(formatRate(-Infinity)).toBe('0 B/s');
		expect(formatRate(-100)).toBe('0 B/s');
	});
});
