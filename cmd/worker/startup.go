package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xgtian-root/aginex/internal/config"
)

const (
	workerStartupPollInterval   = time.Second
	workerStartupRequestTimeout = 3 * time.Second
	workerStartupResponseLimit  = 4 << 10
)

var errInvalidWorkerStartupConfiguration = errors.New(
	"invalid worker startup configuration",
)

type installationStateLoader func() (config.State, error)

// waitForAPIInitialization keeps workers away from the database until the API
// has durably sealed installation, switched to application mode, and reported
// full application readiness. The worker deliberately never applies schema or
// bootstrap changes itself.
func waitForAPIInitialization(
	ctx context.Context,
	loadState installationStateLoader,
	client *http.Client,
	pollInterval time.Duration,
) (config.Config, error) {
	if ctx == nil || loadState == nil || client == nil {
		return config.Config{}, errInvalidWorkerStartupConfiguration
	}
	if pollInterval <= 0 {
		pollInterval = workerStartupPollInterval
	}

	for {
		if err := ctx.Err(); err != nil {
			return config.Config{}, err
		}

		state, err := loadState()
		if err != nil {
			return config.Config{}, fmt.Errorf(
				"load installation state: %w",
				err,
			)
		}

		switch state.Status {
		case config.StatusSetup:
			// The API owns Setup. A worker only waits for its durable marker.
		case config.StatusConfigured:
			if state.Installation == nil {
				return config.Config{}, errInvalidWorkerStartupConfiguration
			}
			if !state.NeedsEnvironmentMarker {
				ready, probeErr := applicationReady(
					ctx,
					client,
					state.Config.HTTP.PublicURL,
				)
				if probeErr != nil {
					return config.Config{}, probeErr
				}
				if ready {
					return state.Config, nil
				}
			}
		default:
			return config.Config{}, errInvalidWorkerStartupConfiguration
		}

		if err := waitForWorkerStartupPoll(ctx, pollInterval); err != nil {
			return config.Config{}, err
		}
	}
}

func applicationReady(
	ctx context.Context,
	client *http.Client,
	publicURL string,
) (bool, error) {
	modeURL, readyURL, err := workerStartupProbeURLs(publicURL)
	if err != nil {
		return false, errInvalidWorkerStartupConfiguration
	}

	modeResponse, ok := workerStartupGET(ctx, client, modeURL)
	if !ok || modeResponse.status != http.StatusOK {
		return false, nil
	}
	var mode struct {
		Mode string `json:"mode"`
	}
	decoder := json.NewDecoder(bytes.NewReader(modeResponse.body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&mode); err != nil || mode.Mode != "application" {
		return false, nil
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return false, nil
	}

	readyResponse, ok := workerStartupGET(ctx, client, readyURL)
	return ok && readyResponse.status == http.StatusOK, nil
}

type workerStartupResponse struct {
	status int
	body   []byte
}

func workerStartupGET(
	ctx context.Context,
	client *http.Client,
	endpoint string,
) (workerStartupResponse, bool) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return workerStartupResponse{}, false
	}
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return workerStartupResponse{}, false
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(
		response.Body,
		workerStartupResponseLimit+1,
	))
	if err != nil || len(body) > workerStartupResponseLimit {
		return workerStartupResponse{}, false
	}
	return workerStartupResponse{status: response.StatusCode, body: body}, true
}

func workerStartupProbeURLs(publicURL string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", errInvalidWorkerStartupConfiguration
	}

	base := strings.TrimRight(parsed.String(), "/")
	modeURL, err := url.JoinPath(base, "/api/v1/system/mode")
	if err != nil {
		return "", "", errInvalidWorkerStartupConfiguration
	}
	readyURL, err := url.JoinPath(base, "/health/ready")
	if err != nil {
		return "", "", errInvalidWorkerStartupConfiguration
	}
	return modeURL, readyURL, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func waitForWorkerStartupPoll(
	ctx context.Context,
	duration time.Duration,
) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
