import { describe, it, expect } from 'vitest';
import { archiveFormat, isArchiveFile, archiveStem } from '$lib/fs/archive';

describe('archiveFormat', () => {
	it('maps each supported extension to its format', () => {
		expect(archiveFormat('x.zip')).toBe('zip');
		expect(archiveFormat('x.tar')).toBe('tar');
		expect(archiveFormat('x.tar.gz')).toBe('targz');
		expect(archiveFormat('x.tgz')).toBe('targz');
	});

	it('is case-insensitive', () => {
		expect(archiveFormat('X.ZIP')).toBe('zip');
		expect(archiveFormat('x.TaR.Gz')).toBe('targz');
		expect(archiveFormat('x.TGZ')).toBe('targz');
	});

	it('prefers tar.gz over tar and zip over tar', () => {
		expect(archiveFormat('x.tar.gz')).toBe('targz');
		expect(archiveFormat('x.zip')).toBe('zip');
	});

	it('returns "" for unsupported or standalone formats', () => {
		for (const name of ['.txt', '.gz', '.bz2', '.xz', '.zst', '.rar', '.7z', 'noext']) {
			expect(archiveFormat(name)).toBe('');
		}
	});
});

describe('isArchiveFile', () => {
	it('is true only for supported archives', () => {
		expect(isArchiveFile('myfiles.zip')).toBe(true);
		expect(isArchiveFile('myfiles.tar.gz')).toBe(true);
		expect(isArchiveFile('myfiles.tgz')).toBe(true);
		expect(isArchiveFile('myfiles.tar')).toBe(true);
		expect(isArchiveFile('notes.txt')).toBe(false);
		expect(isArchiveFile('data.gz')).toBe(false);
		expect(isArchiveFile('readme')).toBe(false);
	});
});

describe('archiveStem', () => {
	it('strips the archive extension', () => {
		expect(archiveStem('myfiles.zip')).toBe('myfiles');
		expect(archiveStem('myfiles.tar')).toBe('myfiles');
		expect(archiveStem('myfiles.tar.gz')).toBe('myfiles');
		expect(archiveStem('myfiles.tgz')).toBe('myfiles');
	});

	it('keeps a middle dot and returns non-archive names unchanged', () => {
		expect(archiveStem('my.backup.zip')).toBe('my.backup');
		expect(archiveStem('notes.txt')).toBe('notes.txt');
		expect(archiveStem('readme')).toBe('readme');
	});
});
