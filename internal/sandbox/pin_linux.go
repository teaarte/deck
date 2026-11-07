//go:build linux

package sandbox

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// pin opens path as the kernel resolves it, through links of the system only; bwrap mounts the opened file itself at
// its real path, so nothing between deck's check and the mount changes what the box sees.
func pin(path string) (*os.File, string, error) {
	fd, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	at, rest, links := "/", parts(path), 0
	fail := func(err error) (*os.File, string, error) {
		_ = unix.Close(fd)
		return nil, "", err
	}
	for len(rest) > 0 {
		c := rest[0]
		rest = rest[1:]
		next, err := unix.Openat(fd, c, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return fail(&os.PathError{Op: "open", Path: filepath.Join(at, c), Err: err})
		}
		var st, dir unix.Stat_t
		if err := unix.Fstat(next, &st); err != nil {
			_ = unix.Close(next)
			return fail(err)
		}
		if st.Mode&unix.S_IFMT != unix.S_IFLNK {
			_ = unix.Close(fd)
			fd, at = next, filepath.Join(at, c)
			continue
		}
		err = unix.Fstat(fd, &dir)
		if links++; err != nil || links > 40 || !rootLink(st.Uid, dir.Uid, uint32(dir.Mode)) {
			_ = unix.Close(next)
			return fail(&Moved{Path: path, Link: filepath.Join(at, c)})
		}
		buf := make([]byte, unix.PathMax)
		n, err := unix.Readlinkat(next, "", buf)
		_ = unix.Close(next)
		if err != nil {
			return fail(err)
		}
		to := string(buf[:n])
		rest = append(parts(to), rest...)
		if filepath.IsAbs(to) {
			_ = unix.Close(fd)
			if fd, err = unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0); err != nil {
				return nil, "", err
			}
			at = "/"
		}
	}
	return os.NewFile(uintptr(fd), at), at, nil
}
