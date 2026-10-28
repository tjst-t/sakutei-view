// Command sakutei-view runs the sakutei-view server: it reads containerlab,
// Sakutei, and (optionally) a device plugin over HTTP, and serves a single
// read-only web page summarizing all three. See README.md.
//
// Configuration is entirely by environment variable (no config file, no
// flags yet) — see README.md for the full list. Outbound connections
// (to clab-api-server, to Sakutei, to the plugin) are each configured with
// a base URL, an optional CA file, and an optional bearer token supplied by
// file path or environment variable name; see internal/httpclient.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tjst-t/sakutei-view/internal/aggregate"
	"github.com/tjst-t/sakutei-view/internal/api"
	"github.com/tjst-t/sakutei-view/internal/httpclient"
	"github.com/tjst-t/sakutei-view/internal/source/containerlab"
	"github.com/tjst-t/sakutei-view/internal/source/plugin"
	"github.com/tjst-t/sakutei-view/internal/source/sakutei"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	addr := getenv("LISTEN_ADDR", "127.0.0.1:8080") // localhost-only by default, per design

	pluginClient, err := buildPluginClient()
	if err != nil {
		return err
	}

	clSource, err := buildContainerlabSource(pluginClient)
	if err != nil {
		return err
	}

	sakuteiClient, err := buildSakuteiClient()
	if err != nil {
		return err
	}

	agg := aggregate.New(clSource, sakuteiClient, pluginClient)
	if d, err := getenvDuration("SOURCE_REQUEST_TIMEOUT"); err != nil {
		return err
	} else if d > 0 {
		agg.RequestTimeout = d
	}
	if d, err := getenvDuration("SOURCE_RECONNECT_INITIAL"); err != nil {
		return err
	} else if d > 0 {
		agg.ReconnectInitial = d
	}
	if d, err := getenvDuration("SOURCE_RECONNECT_MAX"); err != nil {
		return err
	} else if d > 0 {
		agg.ReconnectMax = d
	}

	basicAuthPassword, err := resolveSecret("BASIC_AUTH_PASSWORD_FILE", "BASIC_AUTH_PASSWORD")
	if err != nil {
		return fmt.Errorf("resolving BASIC_AUTH_PASSWORD: %w", err)
	}
	apiServer := &api.Server{
		Aggregator:        agg,
		BasicAuthUser:     os.Getenv("BASIC_AUTH_USER"),
		BasicAuthPassword: basicAuthPassword,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	apiServer.RegisterRoutes(mux)

	log.Printf("sakutei-view listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}

// buildContainerlabSource builds the containerlab.Source selected by
// CONTAINERLAB_SOURCE ("clab-api" or "plugin").
func buildContainerlabSource(pluginClient *plugin.Client) (containerlab.Source, error) {
	mode := os.Getenv("CONTAINERLAB_SOURCE")
	switch mode {
	case "clab-api":
		baseURL := os.Getenv("CONTAINERLAB_BASE_URL")
		labName := os.Getenv("CONTAINERLAB_LAB_NAME")
		if baseURL == "" || labName == "" {
			return nil, fmt.Errorf("CONTAINERLAB_SOURCE=clab-api requires CONTAINERLAB_BASE_URL and CONTAINERLAB_LAB_NAME")
		}
		httpClient, err := httpclient.New(httpclient.Config{
			BaseURL:   baseURL,
			CAFile:    os.Getenv("CONTAINERLAB_CA_FILE"),
			TokenFile: os.Getenv("CONTAINERLAB_TOKEN_FILE"),
			TokenEnv:  os.Getenv("CONTAINERLAB_TOKEN_ENV"),
		})
		if err != nil {
			return nil, fmt.Errorf("configuring containerlab client: %w", err)
		}
		return containerlab.NewClabAPIClient(baseURL, labName, httpClient), nil
	case "plugin":
		if pluginClient == nil {
			return nil, fmt.Errorf("CONTAINERLAB_SOURCE=plugin requires PLUGIN_BASE_URL to also be set")
		}
		return containerlab.NewPluginSource(pluginClient), nil
	default:
		return nil, fmt.Errorf(`CONTAINERLAB_SOURCE must be "clab-api" or "plugin", got %q`, mode)
	}
}

// buildSakuteiClient builds a sakutei.Client if SAKUTEI_BASE_URL is set;
// Sakutei integration is optional (the view still shows containerlab
// topology without it), so an empty value returns (nil, nil).
func buildSakuteiClient() (*sakutei.Client, error) {
	baseURL := os.Getenv("SAKUTEI_BASE_URL")
	if baseURL == "" {
		return nil, nil
	}
	httpClient, err := httpclient.New(httpclient.Config{
		BaseURL:   baseURL,
		CAFile:    os.Getenv("SAKUTEI_CA_FILE"),
		TokenFile: os.Getenv("SAKUTEI_TOKEN_FILE"),
		TokenEnv:  os.Getenv("SAKUTEI_TOKEN_ENV"),
	})
	if err != nil {
		return nil, fmt.Errorf("configuring sakutei client: %w", err)
	}
	return sakutei.NewClient(baseURL, httpClient), nil
}

// buildPluginClient builds a plugin.Client if PLUGIN_BASE_URL is set; the
// device plugin is optional. The same client is reused for
// CONTAINERLAB_SOURCE=plugin and for the plugin's own status/events.
func buildPluginClient() (*plugin.Client, error) {
	baseURL := os.Getenv("PLUGIN_BASE_URL")
	if baseURL == "" {
		return nil, nil
	}
	httpClient, err := httpclient.New(httpclient.Config{
		BaseURL:   baseURL,
		CAFile:    os.Getenv("PLUGIN_CA_FILE"),
		TokenFile: os.Getenv("PLUGIN_TOKEN_FILE"),
		TokenEnv:  os.Getenv("PLUGIN_TOKEN_ENV"),
	})
	if err != nil {
		return nil, fmt.Errorf("configuring plugin client: %w", err)
	}
	return plugin.NewClient(baseURL, httpClient), nil
}

// resolveSecret reads a value from a file (trimmed) if fileEnv names a set
// environment variable, else returns the literal value of valueEnv, else
// "". Mirrors internal/httpclient.Config.Token's file-or-env convention,
// for the one inbound secret (the API's own Basic auth password) this
// binary handles.
func resolveSecret(fileEnvName, valueEnvName string) (string, error) {
	if path := os.Getenv(fileEnvName); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("reading %s (%s): %w", fileEnvName, path, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return os.Getenv(valueEnvName), nil
}

func getenv(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// getenvDuration parses name as a Go duration string (e.g. "10s") if set,
// returning 0 (meaning "use the aggregate package's default") if it is
// unset.
func getenvDuration(name string) (time.Duration, error) {
	v := os.Getenv(name)
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("parsing %s=%q: %w", name, v, err)
	}
	return d, nil
}
