//go:build !linux

package rpc

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func openManagedUnixSocket() (int, error) {
	// Match os/exec's descriptor inheritance boundary on platforms without
	// atomic SOCK_CLOEXEC support.
	syscall.ForkLock.Lock()
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err == nil {
		unix.CloseOnExec(fd)
	}
	syscall.ForkLock.Unlock()
	if err != nil {
		return -1, err
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func chmodManagedSocketAt(dirfd int, path string, mode uint32, flags int) error {
	return unix.Fchmodat(dirfd, path, mode, flags)
}
