package runtime

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"sync"
	"time"

	kitexconfigv1 "github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/configservice"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/diagnosticsservice"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/pagequeryservice"
	"github.com/asherzj/financial_configuration_center/contracts/kitex_gen/finconfig/config/v1/refreshservice"
	platformrpc "github.com/asherzj/financial_configuration_center/internal/platform/rpc"
	"github.com/asherzj/financial_configuration_center/internal/platform/rpcauth"
	"github.com/cloudwego/kitex/server"
)

type HandlerSet struct {
	Config      kitexconfigv1.ConfigService
	PageQuery   kitexconfigv1.PageQueryService
	Refresh     kitexconfigv1.RefreshService
	Diagnostics kitexconfigv1.DiagnosticsService
}

type PrivateServer struct {
	server   server.Server
	listener ownedListener
	stopWait time.Duration

	mu         sync.Mutex
	runStarted bool
	stopped    bool
	accepting  chan struct{}
	runDone    chan struct{}
	acceptOnce sync.Once
	doneOnce   sync.Once
	stopOnce   sync.Once
	stopErr    error
}

var ErrKitexBootstrapStopTimeout = errors.New("timed out waiting for private Kitex bootstrap before Stop")
var ErrKitexRunExitTimeout = errors.New("timed out waiting for private Kitex Run to exit after listener close")

type ownedListener interface {
	net.Listener
	KitexServerOption() server.Option
	Cleanup() error
}

type listenerFactory func(string, os.FileMode, int) (ownedListener, error)
type serviceRegistrar func(server.Server, HandlerSet) error

func NewPrivateServer(socketPath string, mode os.FileMode, groupID int, exitWait time.Duration, authenticator *rpcauth.Authenticator, handlers HandlerSet) (*PrivateServer, error) {
	return newPrivateServer(socketPath, mode, groupID, exitWait, authenticator, handlers, func(path string, mode os.FileMode, groupID int) (ownedListener, error) {
		return platformrpc.ListenPrivateBackend(path, mode, groupID)
	})
}

func newPrivateServer(socketPath string, mode os.FileMode, groupID int, exitWait time.Duration, authenticator *rpcauth.Authenticator, handlers HandlerSet, listen listenerFactory) (*PrivateServer, error) {
	return newPrivateServerWithRegistrar(socketPath, mode, groupID, exitWait, authenticator, handlers, listen, registerServices)
}

func newPrivateServerWithRegistrar(socketPath string, mode os.FileMode, groupID int, exitWait time.Duration, authenticator *rpcauth.Authenticator, handlers HandlerSet, listen listenerFactory, registrar serviceRegistrar) (*PrivateServer, error) {
	if err := validateHandlers(handlers); err != nil {
		return nil, err
	}
	if exitWait < 3*time.Millisecond {
		return nil, errors.New("private Kitex stop timeout must be at least three milliseconds")
	}
	phaseWait := exitWait / 3
	authOptions, err := rpcauth.KitexServerOptions(authenticator)
	if err != nil {
		return nil, err
	}
	if listen == nil {
		return nil, errors.New("private Kitex listener factory is required")
	}
	if registrar == nil {
		return nil, errors.New("private Kitex service registrar is required")
	}
	listener, err := listen(socketPath, mode, groupID)
	if err != nil {
		return nil, err
	}
	if isNil(listener) {
		return nil, errors.New("private Kitex listener factory returned no listener")
	}
	if listener.Addr() == nil || listener.Addr().Network() != "unix" {
		return nil, errors.Join(errors.New("private Kitex listener must use Unix domain sockets"), listener.Close(), listener.Cleanup())
	}
	options := []server.Option{listener.KitexServerOption(), server.WithExitWaitTime(phaseWait)}
	options = append(options, authOptions...)
	kitexServer := server.NewServer(options...)
	if err := safelyRegisterServices(registrar, kitexServer, handlers); err != nil {
		return nil, errors.Join(err, listener.Close(), listener.Cleanup())
	}
	return &PrivateServer{
		server: kitexServer, listener: listener,
		stopWait:  phaseWait,
		accepting: make(chan struct{}), runDone: make(chan struct{}),
	}, nil
}

func validateHandlers(handlers HandlerSet) error {
	for _, required := range []struct {
		name    string
		handler any
	}{
		{name: "ConfigService", handler: handlers.Config},
		{name: "PageQueryService", handler: handlers.PageQuery},
		{name: "RefreshService", handler: handlers.Refresh},
		{name: "DiagnosticsService", handler: handlers.Diagnostics},
	} {
		if isNil(required.handler) {
			return errors.New(required.name + " handler is required")
		}
	}
	return nil
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func registerServices(kitexServer server.Server, handlers HandlerSet) error {
	if err := configservice.RegisterService(kitexServer, handlers.Config); err != nil {
		return fmt.Errorf("register ConfigService: %w", err)
	}
	if err := pagequeryservice.RegisterService(kitexServer, handlers.PageQuery); err != nil {
		return fmt.Errorf("register PageQueryService: %w", err)
	}
	if err := refreshservice.RegisterService(kitexServer, handlers.Refresh); err != nil {
		return fmt.Errorf("register RefreshService: %w", err)
	}
	if err := diagnosticsservice.RegisterService(kitexServer, handlers.Diagnostics); err != nil {
		return fmt.Errorf("register DiagnosticsService: %w", err)
	}
	return nil
}

func safelyRegisterServices(registrar serviceRegistrar, kitexServer server.Server, handlers HandlerSet) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("register Kitex services: %v", recovered)
		}
	}()
	return registrar(kitexServer, handlers)
}

func (server *PrivateServer) Run() error {
	if server == nil || server.server == nil {
		return errors.New("private Kitex server is not constructed")
	}
	server.mu.Lock()
	if server.runStarted {
		server.mu.Unlock()
		return errors.New("private Kitex server Run may only be called once")
	}
	if server.stopped {
		server.mu.Unlock()
		return errors.New("private Kitex server was stopped before Run")
	}
	server.runStarted = true
	server.mu.Unlock()
	go server.probeAccepting()

	runErr := server.server.Run()
	if errors.Is(runErr, net.ErrClosed) {
		runErr = nil
	}
	closeErr := server.listener.Close()
	if errors.Is(closeErr, net.ErrClosed) {
		closeErr = nil
	}
	server.mu.Lock()
	server.stopped = true
	server.mu.Unlock()
	server.doneOnce.Do(func() { close(server.runDone) })
	return errors.Join(runErr, closeErr)
}

func (server *PrivateServer) Stop() error {
	if server == nil || server.server == nil {
		return nil
	}
	server.stopOnce.Do(func() {
		server.mu.Lock()
		runStarted := server.runStarted
		if !runStarted {
			server.stopped = true
		}
		server.mu.Unlock()
		var bootstrapErr error
		listenerClosed := false
		if runStarted {
			timer := time.NewTimer(server.stopWait)
			select {
			case <-server.accepting:
				timer.Stop()
			case <-server.runDone:
				timer.Stop()
			case <-timer.C:
				bootstrapErr = errors.Join(ErrKitexBootstrapStopTimeout, server.listener.Close())
				listenerClosed = true
				exitTimer := time.NewTimer(server.stopWait)
				select {
				case <-server.runDone:
					exitTimer.Stop()
				case <-exitTimer.C:
					bootstrapErr = errors.Join(bootstrapErr, ErrKitexRunExitTimeout)
				}
			}
		}
		stopErr := server.server.Stop()
		var closeErr error
		if !listenerClosed {
			closeErr = server.listener.Close()
		}
		if errors.Is(closeErr, net.ErrClosed) {
			closeErr = nil
		}
		server.stopErr = errors.Join(bootstrapErr, stopErr, closeErr)
		server.mu.Lock()
		server.stopped = true
		server.mu.Unlock()
		if !runStarted {
			server.doneOnce.Do(func() { close(server.runDone) })
		}
	})
	return server.stopErr
}

func (server *PrivateServer) Cleanup() error {
	if server == nil || server.listener == nil {
		return nil
	}
	return server.listener.Cleanup()
}

func (server *PrivateServer) Addr() net.Addr {
	if server == nil || server.listener == nil {
		return nil
	}
	return server.listener.Addr()
}

func (server *PrivateServer) Accepting() <-chan struct{} {
	if server == nil || server.accepting == nil {
		return nil
	}
	return server.accepting
}

func (server *PrivateServer) probeAccepting() {
	address := server.listener.Addr()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if probeHTTP2Server(address.String()) {
			server.acceptOnce.Do(func() { close(server.accepting) })
			return
		}
		select {
		case <-server.runDone:
			return
		case <-ticker.C:
		}
	}
}

func probeHTTP2Server(path string) bool {
	connection, err := net.DialTimeout("unix", path, 50*time.Millisecond)
	if err != nil {
		return false
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		return false
	}
	// RFC 9113 client connection preface followed by an empty SETTINGS frame.
	preface := append([]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"), []byte{0, 0, 0, 4, 0, 0, 0, 0, 0}...)
	if _, err := connection.Write(preface); err != nil {
		return false
	}
	header := make([]byte, 9)
	if _, err := io.ReadFull(connection, header); err != nil {
		return false
	}
	length := int(header[0])<<16 | int(header[1])<<8 | int(header[2])
	streamIDIsZero := header[5]&0x7f == 0 && header[6] == 0 && header[7] == 0 && header[8] == 0
	return header[3] == 4 && streamIDIsZero && length%6 == 0 // SETTINGS is the mandatory first server frame.
}
