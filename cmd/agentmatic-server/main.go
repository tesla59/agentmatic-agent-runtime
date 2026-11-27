package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/client-go/kubernetes"

	"github.com/tesla59/agentmatic-agent-runtime/internal/auth"
	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
	"github.com/tesla59/agentmatic-agent-runtime/internal/logger"
	"github.com/tesla59/agentmatic-agent-runtime/internal/runtime"
	"github.com/tesla59/agentmatic-agent-runtime/internal/server"
	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

const (
	authModeK8s   = "k8s"
	authModeLocal = "local"
)

type serverConfig struct {
	ConfigFile     string
	Addr           string
	SessionDir     string
	AuthMode       string
	ClusterID      string
	Audiences      []string
	AllowedOrigins []string
	KubeConfigPath string
	MaxSteps       int
	MaxOutputBytes int

	// Optional overrides used in tests
	K8sClient kubernetes.Interface
	LLMClient llm.Client
}

func main() {
	logger.Setup(logger.ConfigFromEnv())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := newRootCommand(func(runCtx context.Context, cfg serverConfig) error {
		srv, err := buildServer(runCtx, cfg)
		if err != nil {
			return err
		}
		return srv.ListenAndServe(runCtx)
	})

	if err := cmd.ExecuteContext(ctx); err != nil {
		slog.Error("agentmatic server exited with error", "error", err)
		os.Exit(1)
	}
}

func newRootCommand(runFn func(context.Context, serverConfig) error) *cobra.Command {
	v := viper.New()
	v.SetEnvPrefix("AGENTMATIC")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()

	var cfgFile string
	cmd := &cobra.Command{
		Use:          "agentmatic-server",
		Short:        "AgentMatic centralized Kubernetes runtime daemon",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cfgFile != "" {
				v.SetConfigFile(cfgFile)
				if err := v.ReadInConfig(); err != nil {
					return fmt.Errorf("read config file %q: %w", cfgFile, err)
				}
			}

			cfg := serverConfig{
				ConfigFile:     cfgFile,
				Addr:           v.GetString("addr"),
				SessionDir:     v.GetString("session-dir"),
				AuthMode:       strings.ToLower(strings.TrimSpace(v.GetString("auth-mode"))),
				ClusterID:      strings.TrimSpace(v.GetString("cluster-id")),
				Audiences:      parseCSV(v.GetStringSlice("audiences")),
				AllowedOrigins: parseCSV(v.GetStringSlice("allowed-origins")),
				KubeConfigPath: v.GetString("kubeconfig"),
				MaxSteps:       v.GetInt("max-steps"),
				MaxOutputBytes: v.GetInt("max-output-bytes"),
			}
			return runFn(cmd.Context(), cfg)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&cfgFile, "config", "", "Path to YAML/JSON server config file")
	flags.String("addr", ":8080", "HTTP/ConnectRPC listen address")
	flags.String("session-dir", "", "Directory for persisted sessions (defaults to ~/.agentmatic/sessions)")
	flags.String("auth-mode", authModeK8s, "Authentication mode: 'k8s' (TokenReview + SAR) or 'local' (dev mode)")
	flags.String("cluster-id", "", "Target Kubernetes cluster identifier (required when auth-mode=k8s)")
	flags.StringSlice("audiences", nil, "Comma-separated expected TokenReview audiences")
	flags.StringSlice("allowed-origins", nil, "Comma-separated CORS allowed browser origins (defaults to '*')")
	flags.String("kubeconfig", "", "Optional path to kubeconfig (defaults to in-cluster config, then ~/.kube/config)")
	flags.Int("max-steps", 8, "Maximum reasoning steps per execution turn")
	flags.Int("max-output-bytes", 8192, "Maximum sanitized tool output bytes")

	for _, name := range []string{
		"addr",
		"session-dir",
		"auth-mode",
		"cluster-id",
		"audiences",
		"allowed-origins",
		"kubeconfig",
		"max-steps",
		"max-output-bytes",
	} {
		_ = v.BindPFlag(name, flags.Lookup(name))
	}

	return cmd
}

func buildServer(ctx context.Context, cfg serverConfig) (*server.Server, error) {
	if cfg.AuthMode == "" {
		cfg.AuthMode = authModeK8s
	}

	var enforceRBAC bool
	switch cfg.AuthMode {
	case authModeK8s:
		if cfg.ClusterID == "" {
			return nil, errors.New("cluster-id is required when auth-mode=k8s")
		}
		enforceRBAC = true
	case authModeLocal:
		slog.Warn("running server in local auth mode without TokenReview or Kubernetes RBAC enforcement")
		enforceRBAC = false
	default:
		return nil, fmt.Errorf("unsupported auth-mode %q (expected %q or %q)", cfg.AuthMode, authModeK8s, authModeLocal)
	}

	rt, err := runtime.New(ctx, runtime.Config{
		KubeConfigPath: cfg.KubeConfigPath,
		MaxSteps:       cfg.MaxSteps,
		MaxOutputBytes: cfg.MaxOutputBytes,
		EnforceRBAC:    enforceRBAC,
		K8sClient:      cfg.K8sClient,
		LLMClient:      cfg.LLMClient,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize runtime: %w", err)
	}

	var authn auth.Authenticator
	if cfg.AuthMode == authModeK8s {
		authn, err = auth.NewTokenReviewAuthenticator(auth.TokenReviewConfig{
			Client:    rt.K8s,
			ClusterID: cfg.ClusterID,
			Audiences: cfg.Audiences,
		})
		if err != nil {
			return nil, fmt.Errorf("initialize authenticator: %w", err)
		}
	} else {
		authn = auth.NewLocalDevAuthenticator()
	}

	store, err := session.NewFileStore(cfg.SessionDir)
	if err != nil {
		return nil, fmt.Errorf("initialize session store: %w", err)
	}

	sm := session.NewSessionManager(store)
	if err := sm.LoadAll(ctx, rt.Engine); err != nil {
		slog.Warn("failed to load some sessions from disk", "error", err)
	}

	srv, err := server.New(server.Config{
		Addr:           cfg.Addr,
		Runtime:        rt,
		SessionManager: sm,
		Authenticator:  authn,
		AllowedOrigins: cfg.AllowedOrigins,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize server: %w", err)
	}

	return srv, nil
}

func parseCSV(items []string) []string {
	var out []string
	for _, item := range items {
		for part := range strings.SplitSeq(item, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out
}
