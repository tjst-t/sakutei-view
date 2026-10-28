// Package httpclient builds *http.Client values for the outbound connections
// sakutei-view makes (to Sakutei's --read-listen API, to a device plugin, and
// to containerlab/clab-api-server). Every such connection is configured the
// same way:
//
//   - a base URL, which may be https
//   - an optional bearer token, supplied via a file path or an environment
//     variable name (never written inline in a config file)
//   - an optional path to a PEM file holding CA certificates to trust; if
//     omitted, the OS trust store is used
//
// There is deliberately no option to disable TLS verification. Servers using
// a private CA must have that CA's certificate made available via CAFile.
package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// Config describes one outbound connection.
type Config struct {
	// BaseURL is the address of the remote API (e.g. "https://sakutei.example:8443").
	BaseURL string

	// TokenFile, if set, is a path to a file whose (trimmed) contents are
	// sent as the bearer token. Takes precedence over TokenEnv.
	TokenFile string

	// TokenEnv, if set, is the name of an environment variable holding the
	// bearer token.
	TokenEnv string

	// CAFile, if set, is a path to a PEM file containing one or more CA
	// certificates to trust for this connection. If empty, the OS trust
	// store is used.
	CAFile string
}

// Token resolves the bearer token for this config, if any is configured.
// It returns ("", nil) when no token source is configured.
func (c Config) Token() (string, error) {
	if c.TokenFile != "" {
		b, err := os.ReadFile(c.TokenFile)
		if err != nil {
			return "", fmt.Errorf("httpclient: reading token file %q: %w", c.TokenFile, err)
		}
		token := strings.TrimSpace(string(b))
		if token == "" {
			return "", fmt.Errorf("httpclient: token file %q is empty", c.TokenFile)
		}
		return token, nil
	}
	if c.TokenEnv != "" {
		token := os.Getenv(c.TokenEnv)
		if token == "" {
			return "", fmt.Errorf("httpclient: environment variable %q is not set", c.TokenEnv)
		}
		return token, nil
	}
	return "", nil
}

// New builds an *http.Client for this config. The returned client has no
// Timeout set, because it may be used for long-lived Server-Sent Events
// streams (callers that want a per-request deadline should use a context).
func New(cfg Config) (*http.Client, error) {
	tlsConfig := &tls.Config{}

	if cfg.CAFile != "" {
		pemBytes, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("httpclient: reading CA file %q: %w", cfg.CAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("httpclient: no certificates found in CA file %q", cfg.CAFile)
		}
		tlsConfig.RootCAs = pool
	}
	// When CAFile is empty, tlsConfig.RootCAs stays nil, which makes the Go
	// TLS stack fall back to the OS trust store. There is no setting here
	// to skip verification.

	token, err := cfg.Token()
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{TLSClientConfig: tlsConfig}

	var rt http.RoundTripper = transport
	if token != "" {
		rt = &bearerRoundTripper{token: token, next: transport}
	}

	return &http.Client{Transport: rt}, nil
}

// bearerRoundTripper adds an "Authorization: Bearer <token>" header to every
// outgoing request. It clones the request before mutating it, per the
// http.RoundTripper contract.
type bearerRoundTripper struct {
	token string
	next  http.RoundTripper
}

func (rt *bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header.Set("Authorization", "Bearer "+rt.token)
	return rt.next.RoundTrip(cloned)
}
