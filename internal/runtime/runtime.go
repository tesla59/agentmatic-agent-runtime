package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"

	"k8s.io/client-go/kubernetes"

	"github.com/tesla59/agentmatic-agent-runtime/internal/engine"
	"github.com/tesla59/agentmatic-agent-runtime/internal/interceptor"
	"github.com/tesla59/agentmatic-agent-runtime/internal/k8s"
	"github.com/tesla59/agentmatic-agent-runtime/internal/llm"
	"github.com/tesla59/agentmatic-agent-runtime/internal/tools"
)

// Config defines options for initializing the AgentMatic runtime.
type Config struct {
	KubeConfigPath string
	MasterURL      string

	// Provider-agnostic LLM options
	LLMProvider string // "gemini", "anthropic", "openai". Defaults to "gemini"
	LLMAPIKey   string
	LLMModel    string

	MaxSteps       int
	MaxOutputBytes int
	StepListener   engine.StepListener

	// Optional client overrides for testing or custom embedding
	K8sClient kubernetes.Interface
	LLMClient llm.Client
}

// Runtime bundles the assembled agent engine, tool registry, and underlying clients.
type Runtime struct {
	K8s      kubernetes.Interface
	Registry *tools.Registry
	LLM      llm.Client
	Engine   *engine.Engine
}

// New initializes all infrastructure components and returns an assembled Runtime.
func New(ctx context.Context, cfg Config) (*Runtime, error) {
	k8sClient := cfg.K8sClient
	if k8sClient == nil {
		var err error
		k8sClient, err = k8s.NewClient(k8s.Config{
			KubeconfigPath: cfg.KubeConfigPath,
			MasterURL:      cfg.MasterURL,
		})
		if err != nil {
			return nil, fmt.Errorf("initialize k8s client: %w", err)
		}
	}

	maxBytes := cfg.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = 8192
	}
	chain := interceptor.NewDefaultChain(maxBytes)

	registry := tools.NewRegistry()
	standardTools := []tools.Tool{
		tools.NewListPodsTool(k8sClient),
		tools.NewGetPodLogsTool(k8sClient),
		tools.NewGetEventsTool(k8sClient),
	}
	for _, t := range standardTools {
		if err := registry.Register(chain.Wrap(t)); err != nil {
			return nil, fmt.Errorf("register tool %s: %w", t.Name(), err)
		}
	}

	llmClient, err := initLLMClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("initialize llm client: %w", err)
	}

	maxSteps := cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 8
	}

	eng, err := engine.New(engine.Config{
		Client:   llmClient,
		Registry: registry,
		MaxSteps: maxSteps,
		OnStep:   cfg.StepListener,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize engine: %w", err)
	}

	return &Runtime{
		K8s:      k8sClient,
		Registry: registry,
		LLM:      llmClient,
		Engine:   eng,
	}, nil
}

func initLLMClient(ctx context.Context, cfg Config) (llm.Client, error) {
	if cfg.LLMClient != nil {
		return cfg.LLMClient, nil
	}

	provider := cfg.LLMProvider
	if provider == "" {
		provider = os.Getenv("LLM_PROVIDER")
	}
	if provider == "" {
		provider = "gemini"
	}

	switch strings.ToLower(provider) {
	case "gemini":
		apiKey := cfg.LLMAPIKey
		if apiKey == "" {
			apiKey = os.Getenv("GEMINI_API_KEY")
		}
		if apiKey == "" {
			apiKey = os.Getenv("LLM_API_KEY")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("gemini API key is required (set GEMINI_API_KEY or LLM_API_KEY)")
		}

		model := cfg.LLMModel
		if model == "" {
			model = os.Getenv("GEMINI_MODEL")
		}
		if model == "" {
			model = os.Getenv("LLM_MODEL")
		}
		if model == "" {
			model = "gemini-flash-latest"
		}

		return llm.NewGeminiClient(ctx, llm.GeminiConfig{
			APIKey: apiKey,
			Model:  model,
		})
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %q", provider)
	}
}
