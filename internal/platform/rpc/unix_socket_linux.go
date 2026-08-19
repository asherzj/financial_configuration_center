//go:build linux

package rpc

import "golang.org/x/sys/unix"

func openManagedUnixSocket() (int, error) {
	return unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
}

func chmodManagedSocketAt(dirfd int, path string, mode uint32, _ int) error {
	// AT_SYMLINK_NOFOLLOW for chmod requires fchmodat2 (Linux 6.5+). The
	// service-owned, group/other-nonwritable parent is the no-replacement
	// boundary; the caller additionally verifies type and inode before/after.
	return unix.Fchmodat(dirfd, path, mode, 0)
}
