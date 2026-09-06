package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xgtian-root/aginex/server/internal/buildinfo"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/platform/database"
	"gorm.io/gorm"
)

const (
	workerHostStartupPollInterval   = time.Second
	workerHostStartupRequestTimeout = 3 * time.Second
	workerHostStartupResponseLimit  = 4 << 10
)

var errInvalidWorkerHostStartupConfiguration = errors.New(
	"invalid worker startup configuration",
)

// RunWorker owns the complete Aginex durable-worker process lifecycle for
// definition.
//
// It waits until the API has durably completed Setup and reports both
// application mode and readiness before opening the database. Cancel ctx to
// drain active jobs and stop application-module lifecycle hooks.
func (definition Definition) RunWorker(ctx context.Context) error {
	if ctx == nil {
		return errors.New("run worker context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	configureAPIHostLogger()
	return runWorkerHost(ctx, definition, workerHostDependencies{
		loadState:    config.LoadState,
		client:       newWorkerHostHTTPClient(),
		pollInterval: workerHostStartupPollInterval,
		openDatabase: openWorkerHostDatabase,
		newRuntime: func(
			ctx context.Context,
			cfg config.Config,
			db *gorm.DB,
		) (workerHostRuntime, error) {
			return definition.NewWorker(ctx, cfg, db)
		},
	})
}

type workerHostStateLoader func() (config.State, error)

type workerHostDatabase struct {
	database *gorm.DB
	close    func() error
}

type workerHostRuntime interface {
	Start(context.Context) error
	Ready(context.Context) error
	Run(context.Context) error
	Shutdown(context.Context) error
}

type workerHostDependencies struct {
	loadState    workerHostStateLoader
	client       *http.Client
	pollInterval time.Duration
	openDatabase func(context.Context, config.Database) (workerHostDatabase, error)
	newRuntime   func(context.Context, config.Config, *gorm.DB) (workerHostRuntime, error)
}

func runWorkerHost(
	ctx context.Context,
	definition Definition,
	dependencies workerHostDependencies,
) (result error) {
	if ctx == nil ||
		dependencies.loadState == nil ||
		dependencies.client == nil ||
		dependencies.openDatabase == nil ||
		dependencies.newRuntime == nil {
		return errInvalidWorkerHostStartupConfiguration
	}

	slog.Info("Aginex worker waiting for API initialization")
	cfg, err := waitForWorkerAPIInitialization(
		ctx,
		dependencies.loadState,
		dependencies.client,
		dependencies.pollInterval,
	)
	if err != nil {
		if err == context.Canceled && ctx.Err() == context.Canceled {
			return nil
		}
		return fmt.Errorf("wait for API initialization: %w", err)
	}

	opened, err := dependencies.openDatabase(ctx, cfg.Database)
	if err != nil {
		return err
	}
	if opened.database == nil || opened.close == nil {
		return errInvalidWorkerHostStartupConfiguration
	}
	defer func() {
		if closeErr := opened.close(); closeErr != nil {
			result = errors.Join(
				result,
				fmt.Errorf("close worker database: %w", closeErr),
			)
		}
	}()

	runtime, err := dependencies.newRuntime(ctx, cfg, opened.database)
	if err != nil {
		return fmt.Errorf("create worker: %w", err)
	}
	if runtime == nil {
		return errInvalidWorkerHostStartupConfiguration
	}
	if err := runtime.Start(ctx); err != nil {
		return fmt.Errorf("start worker lifecycle: %w", err)
	}
	// Runtime.Run normally owns shutdown. Keep a host-level guard as well so a
	// cancellation in the Ready-to-Run handoff cannot strand already-started
	// module hooks. Runtime shutdown is idempotent; avoid joining its cached
	// error twice when Run already returned it.
	defer func() {
		shutdownContext, cancel := context.WithTimeout(
			context.Background(),
			workerHostShutdownGracePeriod(cfg),
		)
		shutdownErr := runtime.Shutdown(shutdownContext)
		cancel()
		if shutdownErr != nil && !errors.Is(result, shutdownErr) {
			result = errors.Join(
				result,
				fmt.Errorf("stop worker lifecycle: %w", shutdownErr),
			)
		}
	}()
	if err := runtime.Ready(ctx); err != nil {
		return fmt.Errorf("worker is not ready: %w", err)
	}

	slog.Info(
		"Aginex worker started",
		"worker_id", cfg.Jobs.WorkerID,
		"concurrency", cfg.Jobs.Concurrency,
		"version", buildinfo.Version,
		"commit", buildinfo.Commit,
		"build_date", buildinfo.BuildDate,
		"module_fingerprint", definition.Fingerprint(),
	)
	if err := runtime.Run(ctx); err != nil {
		if err == context.Canceled && ctx.Err() == context.Canceled {
			return nil
		}
		return fmt.Errorf("run worker: %w", err)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("run worker: %w", ctx.Err())
	}
	slog.Info("Aginex worker stopped", "worker_id", cfg.Jobs.WorkerID)
	return nil
}

func openWorkerHostDatabase(
	ctx context.Context,
	cfg config.Database,
) (workerHostDatabase, error) {
	db, err := database.OpenContext(ctx, cfg)
	if err != nil {
		return workerHostDatabase{}, fmt.Errorf("open database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return workerHostDatabase{}, fmt.Errorf(
			"access database connection: %w",
			err,
		)
	}
	return workerHostDatabase{
		database: db,
		close:    sqlDB.Close,
	}, nil
}

func workerHostShutdownGracePeriod(cfg config.Config) time.Duration {
	return config.WithDefaults(cfg).HTTP.ShutdownGracePeriod
}

func newWorkerHostHTTPClient() *http.Client {
	return &http.Client{
		Timeout: workerHostStartupRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// waitForWorkerAPIInitialization keeps workers away from the database until
// the API has sealed installation, switched to application mode, and reported
// full application readiness. The worker deliberately never mutates schema or
// installation state itself.
func waitForWorkerAPIInitialization(
	ctx context.Context,
	loadState workerHostStateLoader,
	client *http.Client,
	pollInterval time.Duration,
) (config.Config, error) {
	if ctx == nil || loadState == nil || client == nil {
		return config.Config{}, errInvalidWorkerHostStartupConfiguration
	}
	if pollInterval <= 0 {
		pollInterval = workerHostStartupPollInterval
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
				return config.Config{}, errInvalidWorkerHostStartupConfiguration
			}
			if !state.NeedsEnvironmentMarker {
				ready, probeErr := workerHostApplicationReady(
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
			return config.Config{}, errInvalidWorkerHostStartupConfiguration
		}

		if err := waitForWorkerHostPoll(ctx, pollInterval); err != nil {
			return config.Config{}, err
		}
	}
}

func workerHostApplicationReady(
	ctx context.Context,
	client *http.Client,
	publicURL string,
) (bool, error) {
	modeURL, readyURL, err := workerHostProbeURLs(publicURL)
	if err != nil {
		return false, errInvalidWorkerHostStartupConfiguration
	}

	modeResponse, ok := workerHostGET(ctx, client, modeURL)
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
	if err := ensureWorkerHostJSONEnd(decoder); err != nil {
		return false, nil
	}

	readyResponse, ok := workerHostGET(ctx, client, readyURL)
	return ok && readyResponse.status == http.StatusOK, nil
}

type workerHostResponse struct {
	status int
	body   []byte
}

func workerHostGET(
	ctx context.Context,
	client *http.Client,
	endpoint string,
) (workerHostResponse, bool) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return workerHostResponse{}, false
	}
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return workerHostResponse{}, false
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(
		response.Body,
		workerHostStartupResponseLimit+1,
	))
	if err != nil || len(body) > workerHostStartupResponseLimit {
		return workerHostResponse{}, false
	}
	return workerHostResponse{status: response.StatusCode, body: body}, true
}

func workerHostProbeURLs(publicURL string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", errInvalidWorkerHostStartupConfiguration
	}

	base := strings.TrimRight(parsed.String(), "/")
	modeURL, err := url.JoinPath(base, "/api/v1/system/mode")
	if err != nil {
		return "", "", errInvalidWorkerHostStartupConfiguration
	}
	readyURL, err := url.JoinPath(base, "/health/ready")
	if err != nil {
		return "", "", errInvalidWorkerHostStartupConfiguration
	}
	return modeURL, readyURL, nil
}

func ensureWorkerHostJSONEnd(decoder *json.Decoder) error {
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

func waitForWorkerHostPoll(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
