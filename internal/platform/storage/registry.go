package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/xgtian-root/aginex/framework/observability"
	"github.com/xgtian-root/aginex/internal/config"
)

var (
	ErrStorageProfileNotFound       = errors.New("storage profile not found")
	ErrStorageProfileDegraded       = errors.New("storage profile is unavailable")
	ErrStorageProfileAmbiguous      = errors.New("storage profile mapping is ambiguous")
	ErrStorageCapabilityUnavailable = errors.New("storage capability is unavailable")
)

type registryEntry struct {
	profile config.StorageProfile
	store   Storage
	err     error
}

// Registry owns the immutable set of stores loaded at process startup. A
// configuration update intentionally does not mutate this registry: API and
// worker restart together before the new active profile is used.
type Registry struct {
	mu       sync.RWMutex
	activeID string
	entries  map[string]registryEntry
}

func NewRegistry(
	ctx context.Context,
	cfg config.Config,
	recorder *observability.Recorder,
	policies ...FilePolicy,
) (*Registry, error) {
	runtime := cfg.StorageRuntime()
	registry := &Registry{
		activeID: runtime.ActiveProfileID,
		entries:  make(map[string]registryEntry, len(runtime.Profiles)),
	}
	for _, profile := range runtime.Profiles {
		storageConfig := profile.StorageConfig()
		store, err := FromConfig(ctx, storageConfig, cfg.HTTP.PublicURL, policies...)
		if err == nil {
			store = Observe(store, storageConfig.Driver, recorder)
		}
		registry.entries[profile.ID] = registryEntry{
			profile: profile,
			store:   store,
			err:     err,
		}
	}
	if _, err := registry.Resolve(runtime.ActiveProfileID); err != nil {
		return nil, fmt.Errorf("configure active storage profile: %w", err)
	}
	return registry, nil
}

func (registry *Registry) ActiveMultipart() (MultipartObjectStore, error) {
	if registry == nil {
		return nil, ErrStorageProfileNotFound
	}
	return registry.ResolveMultipart(registry.activeID)
}

func (registry *Registry) ResolveMultipart(profileID string) (MultipartObjectStore, error) {
	store, err := registry.Resolve(profileID)
	if err != nil {
		return nil, err
	}
	multipart, ok := AsMultipart(store)
	if !ok {
		return nil, ErrStorageCapabilityUnavailable
	}
	return multipart, nil
}

func (registry *Registry) ActiveControlledRead() (ControlledRead, error) {
	if registry == nil {
		return nil, ErrStorageProfileNotFound
	}
	return registry.ResolveControlledRead(registry.activeID)
}

func (registry *Registry) ResolveControlledRead(profileID string) (ControlledRead, error) {
	store, err := registry.Resolve(profileID)
	if err != nil {
		return nil, err
	}
	controlled, ok := AsControlledRead(store)
	if !ok {
		return nil, ErrStorageCapabilityUnavailable
	}
	return controlled, nil
}

func (registry *Registry) Active() (Storage, error) {
	if registry == nil {
		return nil, ErrStorageProfileNotFound
	}
	return registry.Resolve(registry.activeID)
}

func (registry *Registry) ActiveProfile() (config.StorageProfile, bool) {
	if registry == nil {
		return config.StorageProfile{}, false
	}
	return registry.Profile(registry.activeID)
}

func (registry *Registry) Resolve(profileID string) (Storage, error) {
	if registry == nil || strings.TrimSpace(profileID) == "" {
		return nil, ErrStorageProfileNotFound
	}
	registry.mu.RLock()
	entry, ok := registry.entries[profileID]
	registry.mu.RUnlock()
	if !ok {
		return nil, ErrStorageProfileNotFound
	}
	if entry.err != nil || entry.store == nil {
		return nil, fmt.Errorf("%w: %s", ErrStorageProfileDegraded, profileID)
	}
	return entry.store, nil
}

func (registry *Registry) Profile(profileID string) (config.StorageProfile, bool) {
	if registry == nil {
		return config.StorageProfile{}, false
	}
	registry.mu.RLock()
	entry, ok := registry.entries[profileID]
	registry.mu.RUnlock()
	return entry.profile, ok
}

// ResolveLegacy maps old provider+bucket rows only when exactly one loaded
// profile matches. It deliberately fails closed instead of guessing.
func (registry *Registry) ResolveLegacy(provider, bucket string) (string, Storage, error) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	var match registryEntry
	matchID := ""
	for id, entry := range registry.entries {
		storageConfig := entry.profile.StorageConfig()
		if storageConfig.Driver != provider || storageConfig.Bucket != bucket {
			continue
		}
		if matchID != "" {
			return "", nil, ErrStorageProfileAmbiguous
		}
		matchID = id
		match = entry
	}
	if matchID == "" {
		return "", nil, ErrStorageProfileNotFound
	}
	if match.err != nil || match.store == nil {
		return "", nil, fmt.Errorf("%w: %s", ErrStorageProfileDegraded, matchID)
	}
	return matchID, match.store, nil
}

func (registry *Registry) ResolveProviderUnique(provider string) (string, Storage, error) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	var match registryEntry
	matchID := ""
	for id, entry := range registry.entries {
		if entry.profile.StorageConfig().Driver != provider {
			continue
		}
		if matchID != "" {
			return "", nil, ErrStorageProfileAmbiguous
		}
		matchID = id
		match = entry
	}
	if matchID == "" {
		return "", nil, ErrStorageProfileNotFound
	}
	if match.err != nil || match.store == nil {
		return "", nil, fmt.Errorf("%w: %s", ErrStorageProfileDegraded, matchID)
	}
	return matchID, match.store, nil
}

func (registry *Registry) Profiles() []config.StorageProfile {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	profiles := make([]config.StorageProfile, 0, len(registry.entries))
	for _, entry := range registry.entries {
		profiles = append(profiles, entry.profile)
	}
	registry.mu.RUnlock()
	return config.SortedStorageProfiles(profiles)
}
