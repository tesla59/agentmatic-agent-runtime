package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/tesla59/agentmatic-agent-runtime/internal/logger"
	"github.com/tesla59/agentmatic-agent-runtime/internal/runtime"
	"github.com/tesla59/agentmatic-agent-runtime/internal/session"
)

type cliConfig struct {
	ServerURL      string
	Local          bool
	Token          string
	KubeConfigPath string
	KubeContext    string
	SessionRef     string
	SessionDir     string
	MaxSteps       int
	MaxOutputBytes int
}

func main() {
	logger.Setup(logger.ConfigFromEnv())
	ctx := context.Background()

	cmd := newCLIRootCommand(func(runCtx context.Context, cfg cliConfig, args []string) error {
		client, err := buildAgentClient(runCtx, cfg, nil)
		if err != nil {
			return err
		}
		if len(args) > 0 {
			return runOneShot(runCtx, client, cfg.SessionRef, strings.Join(args, " "))
		}
		return runREPL(runCtx, client, cfg.SessionRef)
	})

	if err := cmd.ExecuteContext(ctx); err != nil {
		slog.Error("execution failed", "error", err)
		os.Exit(1)
	}
}

func newCLIRootCommand(runFn func(context.Context, cliConfig, []string) error) *cobra.Command {
	v := viper.New()
	v.SetEnvPrefix("AGENTMATIC")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()

	var cfgFile string
	cmd := &cobra.Command{
		Use:          "agentmatic [prompt...]",
		Short:        "AgentMatic Kubernetes DevOps CLI client",
		Args:         cobra.ArbitraryArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := loadCLIConfig(v, cfgFile); err != nil {
				return err
			}

			cfg := cliConfig{
				ServerURL:      strings.TrimSpace(v.GetString("server")),
				Local:          v.GetBool("local"),
				Token:          strings.TrimSpace(v.GetString("token")),
				KubeConfigPath: strings.TrimSpace(v.GetString("kubeconfig")),
				KubeContext:    strings.TrimSpace(v.GetString("context")),
				SessionRef:     strings.TrimSpace(v.GetString("session")),
				SessionDir:     strings.TrimSpace(v.GetString("session-dir")),
				MaxSteps:       v.GetInt("max-steps"),
				MaxOutputBytes: v.GetInt("max-output-bytes"),
			}

			if cfg.Local && cfg.ServerURL != "" {
				return errors.New("--local and --server cannot be used together")
			}

			return runFn(cmd.Context(), cfg, args)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&cfgFile, "config", "", "Path to client config file (defaults to ~/.agentmatic/config.yaml if present)")
	flags.String("server", "", "AgentMatic server URL (e.g. http://localhost:8080)")
	flags.Bool("local", false, "Run runtime locally on-host instead of connecting to a server")
	flags.String("token", "", "Bearer token for authenticating to the AgentMatic server")
	flags.String("kubeconfig", "", "Path to kubeconfig for token resolution or local execution")
	flags.String("context", "", "Kubeconfig context name for token resolution")
	flags.String("session", "", "Existing session ID or slug name to attach to")
	flags.String("session-dir", "", "Local session storage directory when running in --local mode")
	flags.Int("max-steps", 8, "Maximum reasoning steps in --local mode")
	flags.Int("max-output-bytes", 8192, "Maximum sanitized tool output bytes in --local mode")

	for _, name := range []string{
		"server",
		"local",
		"token",
		"kubeconfig",
		"context",
		"session",
		"session-dir",
		"max-steps",
		"max-output-bytes",
	} {
		_ = v.BindPFlag(name, flags.Lookup(name))
	}

	return cmd
}

func loadCLIConfig(v *viper.Viper, explicitFile string) error {
	if explicitFile != "" {
		v.SetConfigFile(explicitFile)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("read config file %q: %w", explicitFile, err)
		}
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	v.AddConfigPath(filepath.Join(home, ".agentmatic"))
	v.SetConfigName("config")
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("read default config file: %w", err)
	}
	return nil
}

func buildAgentClient(ctx context.Context, cfg cliConfig, httpClientOverride connect.HTTPClient) (AgentClient, error) {
	if cfg.Local && cfg.ServerURL != "" {
		return nil, errors.New("--local and --server cannot be used together")
	}

	if !cfg.Local && cfg.ServerURL != "" {
		token, err := resolveBearerToken(cfg.Token, cfg.KubeConfigPath, cfg.KubeContext)
		if err != nil {
			return nil, fmt.Errorf("resolve client credentials: %w", err)
		}
		return newRemoteClient(cfg.ServerURL, token, httpClientOverride), nil
	}

	if !cfg.Local {
		slog.Debug("no --server configured; defaulting to on-host local debug mode")
	}

	rt, err := runtime.New(ctx, runtime.Config{
		KubeConfigPath: cfg.KubeConfigPath,
		MaxSteps:       cfg.MaxSteps,
		MaxOutputBytes: cfg.MaxOutputBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize local runtime: %w", err)
	}

	store, err := session.NewFileStore(cfg.SessionDir)
	if err != nil {
		return nil, fmt.Errorf("initialize local session store: %w", err)
	}

	sm := session.NewSessionManager(store)
	if err := sm.LoadAll(ctx, rt.Engine); err != nil {
		slog.Warn("failed to load some local sessions from disk", "error", err)
	}

	return newLocalClient(rt, sm, session.LocalDevIdentity()), nil
}
