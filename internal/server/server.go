package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/tesla59/agentmatic-agent-runtime/gen/agentmatic/v1/agentmaticv1connect"
	"github.com/tesla59/agentmatic-agent-runtime/internal/auth"
	"github.com/tesla59/agentmatic-agent-runtime/internal/runtime"
	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

const (
	defaultAddr              = ":8080"
	defaultReadHeaderTimeout = 10 * time.Second
	defaultShutdownTimeout   = 10 * time.Second
)

// Config defines dependencies and HTTP/CORS settings for the ConnectRPC Server.
type Config struct {
	Addr              string
	Runtime           *runtime.Runtime
	SessionManager    *session.SessionManager
	Authenticator     auth.Authenticator
	AllowedOrigins    []string // Defaults to wildcard "*" when empty
	ReadHeaderTimeout time.Duration
	ShutdownTimeout   time.Duration
}

// Server exposes SessionService and RuntimeService over Connect, gRPC, and gRPC-Web.
type Server struct {
	addr              string
	handler           http.Handler
	readHeaderTimeout time.Duration
	shutdownTimeout   time.Duration
}

// New validates configuration and constructs an assembled ConnectRPC HTTP/2 server.
func New(cfg Config) (*Server, error) {
	if cfg.Runtime == nil || cfg.Runtime.Engine == nil {
		return nil, errors.New("runtime with initialized engine is required")
	}
	if cfg.SessionManager == nil {
		return nil, errors.New("session manager is required")
	}
	if cfg.Authenticator == nil {
		return nil, errors.New("authenticator is required")
	}

	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		addr = defaultAddr
	}
	readHeaderTimeout := cfg.ReadHeaderTimeout
	if readHeaderTimeout <= 0 {
		readHeaderTimeout = defaultReadHeaderTimeout
	}
	shutdownTimeout := cfg.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultShutdownTimeout
	}

	authInterceptor, err := NewAuthInterceptor(cfg.Authenticator)
	if err != nil {
		return nil, fmt.Errorf("create auth interceptor: %w", err)
	}

	sessSvc, err := NewSessionService(cfg.SessionManager, cfg.Runtime.Engine)
	if err != nil {
		return nil, fmt.Errorf("create session service: %w", err)
	}
	rtSvc, err := NewRuntimeService(cfg.SessionManager, cfg.Runtime.Engine)
	if err != nil {
		return nil, fmt.Errorf("create runtime service: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)

	interceptors := connect.WithInterceptors(authInterceptor)
	sessPath, sessHandler := agentmaticv1connect.NewSessionServiceHandler(sessSvc, interceptors)
	rtPath, rtHandler := agentmaticv1connect.NewRuntimeServiceHandler(rtSvc, interceptors)
	mux.Handle(sessPath, sessHandler)
	mux.Handle(rtPath, rtHandler)

	corsWrapped := corsMiddleware(cfg.AllowedOrigins, mux)

	return &Server{
		addr:              addr,
		handler:           corsWrapped,
		readHeaderTimeout: readHeaderTimeout,
		shutdownTimeout:   shutdownTimeout,
	}, nil
}

// Addr returns the configured listen address.
func (s *Server) Addr() string {
	return s.addr
}

// Handler returns the assembled HTTP handler with CORS, health probe, and ConnectRPC routes.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// ListenAndServe starts the HTTP/1.1 + HTTP/2 (h2c) server and blocks until ctx is cancelled or a fatal server error occurs.
func (s *Server) ListenAndServe(ctx context.Context) error {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	httpSrv := &http.Server{
		Addr:              s.addr,
		Handler:           s.handler,
		Protocols:         protocols,
		ReadHeaderTimeout: s.readHeaderTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("starting agentmatic server", "addr", s.addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down agentmatic server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return <-errCh
	}
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func corsMiddleware(allowedOrigins []string, next http.Handler) http.Handler {
	origins := append([]string(nil), allowedOrigins...)
	allowAny := len(origins) == 0 || slices.Contains(origins, "*")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowAny {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if origin != "" && slices.Contains(origins, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}

		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms, Grpc-Timeout, X-Grpc-Web, X-User-Agent")
		w.Header().Set("Access-Control-Expose-Headers", "Connect-Protocol-Version, Grpc-Status, Grpc-Message, Grpc-Status-Details-Bin, "+SessionHeaderKey)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
