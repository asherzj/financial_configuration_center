package rpc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cloudwego/kitex/server"
	"golang.org/x/sys/unix"
)

const privateBackendRoot = "/var/run/finconfig"

type fileIdentity struct {
	device uint64
	inode  uint64
}

type unixPermissionOps struct {
	fchownat func(dirfd int, path string, uid int, gid int, flags int) error
	fchmodat func(dirfd int, path string, mode uint32, flags int) error
}

var defaultUnixPermissionOps = unixPermissionOps{
	fchownat: unix.Fchownat,
	fchmodat: chmodManagedSocketAt,
}

// ManagedUnixListener keeps socket-file ownership separate from the listener
// descriptor lifecycle. Kitex may close the listener; the composition root
// later calls Cleanup after all shutdown phases complete.
type ManagedUnixListener struct {
	listener *net.UnixListener
	parent   *os.File
	baseName string
	identity fileIdentity

	mu         sync.Mutex
	closed     bool
	closeErr   error
	cleanupErr error
	cleaned    bool
}

func ValidatePrivateBackendPath(socketPath string) error {
	return validateManagedUnixPath(socketPath, privateBackendRoot)
}

func ParsePrivateBackendMode(value string) (os.FileMode, error) {
	if len(value) != 4 || value[0] != '0' {
		return 0, errors.New("managed Unix socket mode must be a four-digit octal string")
	}
	parsed, err := strconv.ParseUint(value, 8, 32)
	if err != nil {
		return 0, errors.New("managed Unix socket mode must be a four-digit octal string")
	}
	mode := os.FileMode(parsed)
	if err := ValidatePrivateBackendPermissions(mode, 0); err != nil {
		return 0, err
	}
	return mode, nil
}

func ValidatePrivateBackendPermissions(mode os.FileMode, groupID int) error {
	if mode&0o620 != 0o620 || mode&^os.FileMode(0o660) != 0 {
		return errors.New("managed Unix socket mode must grant owner read/write and group write, and may additionally grant group read")
	}
	// POSIX gid_t is unsigned 32-bit on supported deployment targets. All-ones
	// is the fchownat sentinel for "do not change", so it is never a real GID.
	if groupID < 0 || uint64(groupID) >= uint64(^uint32(0)) {
		return errors.New("managed Unix socket group ID is outside the supported system range")
	}
	return nil
}

func ListenPrivateBackend(socketPath string, mode os.FileMode, groupID int) (*ManagedUnixListener, error) {
	return listenManagedUnix(socketPath, privateBackendRoot, mode, groupID)
}

func listenManagedUnix(socketPath, allowedRoot string, mode os.FileMode, groupID int) (*ManagedUnixListener, error) {
	return listenManagedUnixWithOps(socketPath, allowedRoot, mode, groupID, defaultUnixPermissionOps)
}

func listenManagedUnixWithOps(socketPath, allowedRoot string, mode os.FileMode, groupID int, permissionOps unixPermissionOps) (*ManagedUnixListener, error) {
	if permissionOps.fchownat == nil || permissionOps.fchmodat == nil {
		return nil, errors.New("managed Unix socket permission operations are incomplete")
	}
	if err := validateManagedUnixPath(socketPath, allowedRoot); err != nil {
		return nil, err
	}
	if err := ValidatePrivateBackendPermissions(mode, groupID); err != nil {
		return nil, err
	}
	parentPath := filepath.Dir(socketPath)
	if err := validatePrivateDirectoryChain(filepath.Clean(allowedRoot), parentPath, groupID); err != nil {
		return nil, err
	}
	parent, parentIdentity, err := openPrivateParent(parentPath)
	if err != nil {
		return nil, err
	}
	baseName := filepath.Base(socketPath)
	failBeforeBind := func(cause error) (*ManagedUnixListener, error) {
		return nil, errors.Join(cause, parent.Close())
	}
	if err := ensureDirectoryPathIdentity(parentPath, parentIdentity); err != nil {
		return failBeforeBind(err)
	}
	if err := prepareSocketTarget(parent, baseName, socketPath); err != nil {
		return failBeforeBind(err)
	}

	fd, err := openManagedUnixSocket()
	if err != nil {
		return failBeforeBind(fmt.Errorf("create managed Unix socket: %w", err))
	}
	fdOpen := true
	closeFD := func() error {
		if fdOpen {
			fdOpen = false
			return unix.Close(fd)
		}
		return nil
	}
	if err := unix.Bind(fd, &unix.SockaddrUnix{Name: socketPath}); err != nil {
		return failBeforeBind(errors.Join(fmt.Errorf("bind managed Unix socket: %w", err), closeFD()))
	}

	identity, identityErr := socketIdentityAt(parent, baseName)
	cleanupFailure := func(cause error) (*ManagedUnixListener, error) {
		cleanupErr := closeFD()
		if identityErr == nil {
			cleanupErr = errors.Join(cleanupErr, removeAtIfIdentityMatches(parent, baseName, identity))
		}
		cleanupErr = errors.Join(cleanupErr, parent.Close())
		return nil, errors.Join(cause, cleanupErr)
	}
	if identityErr != nil {
		return cleanupFailure(fmt.Errorf("stat managed Unix socket after bind: %w", identityErr))
	}
	if err := ensureDirectoryPathIdentity(parentPath, parentIdentity); err != nil {
		return cleanupFailure(err)
	}
	if err := permissionOps.fchownat(int(parent.Fd()), baseName, -1, groupID, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return cleanupFailure(fmt.Errorf("set managed Unix socket group: %w", err))
	}
	if err := ensureSocketIdentityAt(parent, baseName, identity); err != nil {
		return cleanupFailure(err)
	}
	if err := permissionOps.fchmodat(int(parent.Fd()), baseName, uint32(mode.Perm()), unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return cleanupFailure(fmt.Errorf("set managed Unix socket mode: %w", err))
	}
	if err := verifySocketAt(parent, baseName, identity, mode, groupID); err != nil {
		return cleanupFailure(err)
	}
	if err := ensureDirectoryPathIdentity(parentPath, parentIdentity); err != nil {
		return cleanupFailure(err)
	}
	// listen is deliberately last: no peer can enter the backlog before the
	// final group, mode, socket identity, and parent identity checks pass.
	if err := unix.Listen(fd, syscall.SOMAXCONN); err != nil {
		return cleanupFailure(fmt.Errorf("listen on managed Unix socket: %w", err))
	}

	file := os.NewFile(uintptr(fd), "finconfig-private-backend")
	listener, err := net.FileListener(file)
	fileCloseErr := file.Close()
	fdOpen = false
	if err != nil {
		return cleanupFailure(errors.Join(fmt.Errorf("adopt managed Unix socket listener: %w", err), fileCloseErr))
	}
	if fileCloseErr != nil {
		_ = listener.Close()
		return cleanupFailure(fmt.Errorf("close adopted managed Unix socket descriptor: %w", fileCloseErr))
	}
	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		_ = listener.Close()
		return cleanupFailure(errors.New("managed Unix socket listener has an unexpected type"))
	}
	unixListener.SetUnlinkOnClose(false)
	return &ManagedUnixListener{listener: unixListener, parent: parent, baseName: baseName, identity: identity}, nil
}

func validateManagedUnixPath(socketPath, allowedRoot string) error {
	cleanPath := filepath.Clean(socketPath)
	cleanRoot := filepath.Clean(allowedRoot)
	if !filepath.IsAbs(socketPath) || !filepath.IsAbs(allowedRoot) || cleanPath != socketPath || cleanRoot != allowedRoot || filepath.Ext(cleanPath) != ".sock" {
		return errors.New("managed Unix socket must use a normalized absolute .sock path")
	}
	relative, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("managed Unix socket path is outside its allowed root")
	}
	return nil
}

// validatePrivateDirectoryChain makes path-based checks safe by establishing
// that only this service identity can mutate the root and its descendants.
func validatePrivateDirectoryChain(root, parent string, groupID int) error {
	relative, err := filepath.Rel(root, parent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("managed Unix socket parent is outside its allowed root")
	}
	current := root
	parts := []string{}
	if relative != "." {
		parts = strings.Split(relative, string(os.PathSeparator))
	}
	for _, part := range append([]string{""}, parts...) {
		if part != "" {
			current = filepath.Join(current, part)
		}
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("stat managed Unix socket parent: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("managed Unix socket parent must be a real directory, not a symlink")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != os.Geteuid() || uint64(stat.Gid) != uint64(groupID) || info.Mode().Perm()&0o010 == 0 || info.Mode().Perm()&0o022 != 0 {
			return errors.New("managed Unix socket parent must be service-owned, shared-group traversable, and not writable by group or others")
		}
	}
	return nil
}

func openPrivateParent(path string) (*os.File, fileIdentity, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fileIdentity{}, fmt.Errorf("open managed Unix socket parent: %w", err)
	}
	parent := os.NewFile(uintptr(fd), "finconfig-private-backend-parent")
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = parent.Close()
		return nil, fileIdentity{}, fmt.Errorf("stat opened managed Unix socket parent: %w", err)
	}
	return parent, identityOfStat(&stat), nil
}

func ensureDirectoryPathIdentity(path string, expected fileIdentity) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed Unix socket parent identity changed")
	}
	actual, err := identityOfFileInfo(info)
	if err != nil || actual != expected {
		return errors.New("managed Unix socket parent identity changed")
	}
	return nil
}

func prepareSocketTarget(parent *os.File, baseName, dialPath string) error {
	var stat unix.Stat_t
	err := unix.Fstatat(int(parent.Fd()), baseName, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat managed Unix socket target: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFSOCK {
		return errors.New("managed Unix socket target must be absent or a stale socket")
	}
	connection, dialErr := net.DialTimeout("unix", dialPath, 200*time.Millisecond)
	if dialErr == nil {
		_ = connection.Close()
		return errors.New("managed Unix socket target is active")
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) {
		return fmt.Errorf("managed Unix socket target could not be proven stale: %w", dialErr)
	}
	if err := removeAtIfIdentityMatches(parent, baseName, identityOfStat(&stat)); err != nil {
		return fmt.Errorf("remove stale managed Unix socket: %w", err)
	}
	return nil
}

func socketIdentityAt(parent *os.File, baseName string) (fileIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), baseName, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fileIdentity{}, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFSOCK {
		return fileIdentity{}, errors.New("managed Unix socket target is no longer a socket")
	}
	return identityOfStat(&stat), nil
}

func ensureSocketIdentityAt(parent *os.File, baseName string, expected fileIdentity) error {
	actual, err := socketIdentityAt(parent, baseName)
	if err != nil || actual != expected {
		return errors.New("managed Unix socket path ownership changed")
	}
	return nil
}

func verifySocketAt(parent *os.File, baseName string, expected fileIdentity, mode os.FileMode, groupID int) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), baseName, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("verify managed Unix socket permissions: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFSOCK || identityOfStat(&stat) != expected || os.FileMode(stat.Mode).Perm() != mode.Perm() || uint64(stat.Gid) != uint64(groupID) {
		return errors.New("managed Unix socket identity or permissions changed")
	}
	return nil
}

func identityOfFileInfo(info os.FileInfo) (fileIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdentity{}, errors.New("managed Unix socket filesystem identity is unavailable")
	}
	return fileIdentity{device: uint64(stat.Dev), inode: uint64(stat.Ino)}, nil
}

func identityOfStat(stat *unix.Stat_t) fileIdentity {
	return fileIdentity{device: uint64(stat.Dev), inode: uint64(stat.Ino)}
}

func removeAtIfIdentityMatches(parent *os.File, baseName string, expected fileIdentity) error {
	var stat unix.Stat_t
	err := unix.Fstatat(int(parent.Fd()), baseName, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	if identityOfStat(&stat) != expected {
		return errors.New("managed Unix socket path ownership changed")
	}
	// The parent directory is owned by this service identity and cannot be
	// mutated by group/other users, so this pair has no cross-principal race.
	return unix.Unlinkat(int(parent.Fd()), baseName, 0)
}

func (listener *ManagedUnixListener) Accept() (net.Conn, error) {
	return listener.listener.Accept()
}

func (listener *ManagedUnixListener) Addr() net.Addr { return listener.listener.Addr() }

// KitexServerOption keeps the concrete *net.UnixListener adaptation inside
// this ownership boundary; callers never receive a pointer that bypasses the
// managed Close/Cleanup state.
func (listener *ManagedUnixListener) KitexServerOption() server.Option {
	return server.WithListener(listener.listener)
}

func (listener *ManagedUnixListener) Close() error {
	if listener == nil {
		return nil
	}
	listener.mu.Lock()
	defer listener.mu.Unlock()
	if !listener.closed {
		listener.closeErr = listener.listener.Close()
		if errors.Is(listener.closeErr, net.ErrClosed) {
			listener.closeErr = nil
		}
		listener.closed = true
	}
	return listener.closeErr
}

func (listener *ManagedUnixListener) Cleanup() error {
	if listener == nil {
		return nil
	}
	listener.mu.Lock()
	defer listener.mu.Unlock()
	if !listener.closed {
		return errors.New("managed Unix socket listener must be closed before cleanup")
	}
	if !listener.cleaned {
		listener.cleanupErr = removeAtIfIdentityMatches(listener.parent, listener.baseName, listener.identity)
		listener.cleanupErr = errors.Join(listener.cleanupErr, listener.parent.Close())
		listener.cleaned = true
	}
	return listener.cleanupErr
}

var _ net.Listener = (*ManagedUnixListener)(nil)
