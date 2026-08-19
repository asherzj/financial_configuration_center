package rpc

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestManagedUnixListenerRecoversOnlyStaleSocket(t *testing.T) {
	t.Parallel()
	root := shortSocketRoot(t)
	path := filepath.Join(root, "backend.sock")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}

	listener, err := listenManagedUnix(path, root, 0o660, os.Getegid())
	if err != nil {
		t.Fatalf("recover stale socket: %v", err)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode().Perm() != 0o660 {
		t.Fatalf("managed socket mode=%v err=%v", info, err)
	}
	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial managed listener: %v", err)
	}
	_ = connection.Close()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("Kitex/listener close removed socket before final cleanup: %v", err)
	}
	if err := listener.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("owned socket still exists: %v", err)
	}
}

func TestManagedUnixListenerRejectsUnsafeTargets(t *testing.T) {
	t.Parallel()
	t.Run("regular file", func(t *testing.T) {
		root := shortSocketRoot(t)
		path := filepath.Join(root, "backend.sock")
		if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := listenManagedUnix(path, root, 0o660, os.Getegid()); err == nil {
			t.Fatal("regular file target accepted")
		}
	})
	t.Run("target symlink", func(t *testing.T) {
		root := shortSocketRoot(t)
		target := filepath.Join(root, "target")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "backend.sock")
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if _, err := listenManagedUnix(path, root, 0o660, os.Getegid()); err == nil {
			t.Fatal("target symlink accepted")
		}
	})
	t.Run("parent symlink", func(t *testing.T) {
		root := shortSocketRoot(t)
		realParent := filepath.Join(root, "real")
		if err := os.Mkdir(realParent, 0o700); err != nil {
			t.Fatal(err)
		}
		linkedParent := filepath.Join(root, "linked")
		if err := os.Symlink(realParent, linkedParent); err != nil {
			t.Fatal(err)
		}
		if _, err := listenManagedUnix(filepath.Join(linkedParent, "backend.sock"), root, 0o660, os.Getegid()); err == nil {
			t.Fatal("symlink parent accepted")
		}
	})
	t.Run("active socket", func(t *testing.T) {
		root := shortSocketRoot(t)
		path := filepath.Join(root, "backend.sock")
		active, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		defer active.Close()
		if _, err := listenManagedUnix(path, root, 0o660, os.Getegid()); err == nil {
			t.Fatal("active socket accepted")
		}
	})
	t.Run("group writable parent", func(t *testing.T) {
		root := shortSocketRoot(t)
		if err := os.Chmod(root, 0o770); err != nil {
			t.Fatal(err)
		}
		if _, err := listenManagedUnix(filepath.Join(root, "backend.sock"), root, 0o660, os.Getegid()); err == nil {
			t.Fatal("group-writable parent accepted")
		}
	})
	t.Run("different parent group", func(t *testing.T) {
		root := shortSocketRoot(t)
		differentGroup := os.Getegid() + 1
		if differentGroup < 0 {
			differentGroup = 0
		}
		if _, err := listenManagedUnix(filepath.Join(root, "backend.sock"), root, 0o660, differentGroup); err == nil {
			t.Fatal("parent outside the configured shared group accepted")
		}
	})
}

func TestManagedUnixListenerCleanupDoesNotDeleteReplacement(t *testing.T) {
	t.Parallel()
	root := shortSocketRoot(t)
	path := filepath.Join(root, "backend.sock")
	listener, err := listenManagedUnix(path, root, 0o660, os.Getegid())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := listener.Cleanup(); err == nil {
		t.Fatal("replacement identity was treated as owned")
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "replacement" {
		t.Fatalf("replacement was removed or changed: %q, %v", content, err)
	}
}

func TestManagedUnixListenerValidatesModeAndGroup(t *testing.T) {
	t.Parallel()
	root := shortSocketRoot(t)
	for _, mode := range []os.FileMode{0o666, 0o770, 0o600, 0o640, 0o400} {
		if _, err := listenManagedUnix(filepath.Join(root, "backend.sock"), root, mode, os.Getegid()); err == nil {
			t.Fatalf("unsafe socket mode %#o accepted", mode)
		}
	}
	if _, err := listenManagedUnix(filepath.Join(root, "backend.sock"), root, 0o660, -1); err == nil {
		t.Fatal("negative socket group accepted")
	}
	if strconv.IntSize == 64 {
		if err := ValidatePrivateBackendPermissions(0o660, int(uint64(^uint32(0)))); err == nil {
			t.Fatal("gid_t all-ones sentinel accepted")
		}
	}
}

func TestManagedUnixListenerCleansUpPermissionFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(unixPermissionOps) unixPermissionOps
	}{
		{name: "chown", mutate: func(ops unixPermissionOps) unixPermissionOps {
			ops.fchownat = func(int, string, int, int, int) error { return os.ErrPermission }
			return ops
		}},
		{name: "chmod", mutate: func(ops unixPermissionOps) unixPermissionOps {
			ops.fchmodat = func(int, string, uint32, int) error { return os.ErrPermission }
			return ops
		}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := shortSocketRoot(t)
			path := filepath.Join(root, "backend.sock")
			if _, err := listenManagedUnixWithOps(path, root, 0o660, os.Getegid(), test.mutate(defaultUnixPermissionOps)); err == nil {
				t.Fatalf("%s failure accepted", test.name)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("owned socket remains after %s failure: %v", test.name, err)
			}
			listener, err := listenManagedUnix(path, root, 0o660, os.Getegid())
			if err != nil {
				t.Fatalf("listener could not be rebound after %s failure: %v", test.name, err)
			}
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			if err := listener.Cleanup(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestManagedUnixListenerCloseAndCleanupAreConcurrentAndIdempotent(t *testing.T) {
	t.Parallel()
	root := shortSocketRoot(t)
	path := filepath.Join(root, "backend.sock")
	listener, err := listenManagedUnix(path, root, 0o660, os.Getegid())
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Cleanup(); err == nil {
		t.Fatal("cleanup before close was accepted")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("early cleanup changed the socket: %v", err)
	}

	var wait sync.WaitGroup
	for range 8 {
		wait.Add(2)
		go func() {
			defer wait.Done()
			_ = listener.Close()
		}()
		go func() {
			defer wait.Done()
			_ = listener.Cleanup()
		}()
	}
	wait.Wait()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := listener.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket remains after final cleanup: %v", err)
	}
}

func shortSocketRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "finconfig-uds-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(root, -1, os.Getegid()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}
