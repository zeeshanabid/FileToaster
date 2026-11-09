package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// FileInfo describes a single selected path for the information dialog. Size is
// the on-disk size of the file, or the recursive on-disk total (every file in
// every sub-directory) for directories — the space actually allocated, as
// reported by `du`, not the logical byte count. FileCount and DirCount are the
// number of files / sub-directories found within the path, walked to the
// deepest level.
type FileInfo struct {
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Size      int64     `json:"size"`
	FileCount int       `json:"fileCount"`
	DirCount  int       `json:"dirCount"`
	Perms     string    `json:"perms"`
	PermsOct  string    `json:"permsOctal"`
	Created   time.Time `json:"created"`
	Modified  time.Time `json:"modified"`
}

// Info returns the information for path, confined to the allowed roots.
func Info(roots *Roots, path string) (FileInfo, error) {
	target, err := roots.Resolve(path)
	if err != nil {
		return FileInfo{}, fsError(err)
	}
	return infoAt(target)
}

// infoAt builds a FileInfo from an already-resolved, contained path. The size
// (on-disk footprint), file and directory counts are walked recursively
// (symlinked directories are never followed, so a broken or circular link cannot
// loop or escape).
func infoAt(target string) (FileInfo, error) {
	info, err := os.Lstat(target)
	if err != nil {
		return FileInfo{}, fmt.Errorf("%w: could not read the requested path", err)
	}
	kind := "file"
	if info.IsDir() {
		kind = "dir"
	}
	size, files, dirs := walkStats(target, info.IsDir())
	created, modified := entryTimes(target, info)
	return FileInfo{
		Path:      target,
		Name:      filepath.Base(target),
		Type:      kind,
		Size:      size,
		FileCount: files,
		DirCount:  dirs,
		Perms:     permString(info.Mode()),
		PermsOct:  fmt.Sprintf("%04o", info.Mode().Perm()),
		Created:   created,
		Modified:  modified,
	}, nil
}

// walkStats returns the on-disk size (allocated space) of everything under
// target together with the number of files and sub-directories encountered,
// walking to the deepest level. For a file it returns the file's allocated
// blocks with FileCount = 1 and DirCount = 0. Allocation is read from the file's
// stat block count (the value `du` reports), rounding small files up to their
// real footprint instead of using the logical byte length.
func walkStats(target string, isDir bool) (size int64, files, dirs int) {
	info, err := os.Lstat(target)
	if err != nil {
		return 0, 0, 0
	}
	if !isDir {
		return blockBytes(info), 1, 0
	}
	// A directory occupies blocks itself and contributes its own footprint on top
	// of everything below it.
	size = blockBytes(info)
	entries, err := os.ReadDir(target)
	if err != nil {
		return size, 0, 0
	}
	for _, de := range entries {
		deInfo, err := de.Info()
		if err != nil {
			continue
		}
		full := filepath.Join(target, de.Name())
		if de.IsDir() {
			dirs++
			s, f, d := walkStats(full, true)
			size += s
			files += f
			dirs += d
		} else {
			size += blockBytes(deInfo)
			files++
		}
	}
	return size, files, dirs
}

// blockBytes reports the on-disk size of info in bytes, matching what `du`
// shows: the number of 512-byte blocks the entry occupies, times 512. It uses
// the stat block count rather than the apparent length, so a 1-byte file still
// counts as a full block. When the platform does not expose a block count it
// falls back to the logical size.
func blockBytes(info os.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return info.Size()
}

// permString renders the classic ten-char Unix permission string, e.g.
// "drwxr-xr-x" for a directory and "-rw-r--r--" for a regular file.
func permString(mode os.FileMode) string {
	var first byte
	switch mode.Type() & os.ModeType {
	case os.ModeDir:
		first = 'd'
	case os.ModeSymlink:
		first = 'l'
	case os.ModeCharDevice:
		first = 'c'
	case os.ModeDevice:
		first = 'b'
	case os.ModeNamedPipe:
		first = 'p'
	case os.ModeSocket:
		first = 's'
	default:
		first = '-'
	}
	write := func(bit int, ch byte) byte {
		if int(mode)&bit != 0 {
			return ch
		}
		return '-'
	}
	b := [10]byte{first}
	b[1] = write(0o400, 'r')
	b[2] = write(0o200, 'w')
	b[3] = write(0o100, 'x')
	b[4] = write(0o040, 'r')
	b[5] = write(0o020, 'w')
	b[6] = write(0o010, 'x')
	b[7] = write(0o004, 'r')
	b[8] = write(0o002, 'w')
	b[9] = write(0o001, 'x')
	return string(b[:])
}
