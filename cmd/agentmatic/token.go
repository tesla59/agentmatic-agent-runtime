package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"k8s.io/client-go/plugin/pkg/client/auth/exec"
	"k8s.io/client-go/transport"
	"k8s.io/client-go/tools/clientcmd"
)

// resolveBearerToken obtains an Authorization bearer token for remote ConnectRPC calls.
// Priority: explicit token -> kubeconfig BearerToken / BearerTokenFile -> kubeconfig ExecProvider.
// If kubeconfig is missing or uses client certificates only, returns "" so local-auth servers still work.
func resolveBearerToken(explicitToken, kubeconfigPath, kubeContext string) (string, error) {
	if token := strings.TrimSpace(explicitToken); token != "" {
		return token, nil
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if trimmed := strings.TrimSpace(kubeconfigPath); trimmed != "" {
		loadingRules.ExplicitPath = trimmed
	}

	overrides := &clientcmd.ConfigOverrides{}
	if trimmed := strings.TrimSpace(kubeContext); trimmed != "" {
		overrides.CurrentContext = trimmed
	}

	clientCfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
	restCfg, err := clientCfg.ClientConfig()
	if err != nil {
		slog.Debug("skipping kubeconfig bearer token resolution", "error", err)
		return "", nil
	}

	if token := strings.TrimSpace(restCfg.BearerToken); token != "" {
		return token, nil
	}

	if restCfg.BearerTokenFile != "" {
		data, err := os.ReadFile(restCfg.BearerTokenFile)
		if err != nil {
			return "", fmt.Errorf("read kubeconfig bearer token file %q: %w", restCfg.BearerTokenFile, err)
		}
		if token := strings.TrimSpace(string(data)); token != "" {
			return token, nil
		}
	}

	if restCfg.ExecProvider != nil {
		authn, err := exec.GetAuthenticator(restCfg.ExecProvider, nil)
		if err != nil {
			return "", fmt.Errorf("initialize kubeconfig exec auth plugin: %w", err)
		}
		tCfg := &transport.Config{}
		if err := authn.UpdateTransportConfig(tCfg); err != nil {
			return "", fmt.Errorf("configure kubeconfig exec auth plugin: %w", err)
		}
		if tCfg.WrapTransport != nil {
			var capturedAuth string
			captureRT := tCfg.WrapTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
				capturedAuth = r.Header.Get("Authorization")
				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
			}))
			req, _ := http.NewRequest(http.MethodGet, "https://kubernetes.default.svc", nil)
			if _, err := captureRT.RoundTrip(req); err != nil {
				return "", fmt.Errorf("execute kubeconfig exec credential plugin: %w", err)
			}
			if _, tok, ok := strings.Cut(strings.TrimSpace(capturedAuth), " "); ok && tok != "" {
				return strings.TrimSpace(tok), nil
			}
		}
	}

	return "", nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
