package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"
)

type StorageProvider string

const (
	StorageProviderLocal      StorageProvider = "local"
	StorageProviderAlibabaOSS StorageProvider = "aliyun-oss"
	StorageProviderAWSS3      StorageProvider = "aws-s3"
	StorageProviderMinIO      StorageProvider = "minio"
	StorageProviderR2         StorageProvider = "cloudflare-r2"
)

type StorageProfileStatus string

const (
	StorageProfileAvailable StorageProfileStatus = "available"
	StorageProfileArchived  StorageProfileStatus = "archived"
)

type StorageCredentialSource string

const (
	StorageCredentialsNone        StorageCredentialSource = "none"
	StorageCredentialsManaged     StorageCredentialSource = "managed"
	StorageCredentialsEnvironment StorageCredentialSource = "environment"
)

// StorageProfile is a durable storage target. Secret fields are persisted only
// in the private installation document and must never be returned by HTTP.
type StorageProfile struct {
	ID               string                  `json:"id"`
	Name             string                  `json:"name"`
	Provider         StorageProvider         `json:"provider"`
	Status           StorageProfileStatus    `json:"status"`
	Bucket           string                  `json:"bucket,omitempty"`
	Region           string                  `json:"region,omitempty"`
	Endpoint         string                  `json:"endpoint,omitempty"`
	AccessBaseURL    string                  `json:"accessBaseUrl,omitempty"`
	AccountID        string                  `json:"accountId,omitempty"`
	LocalRoot        string                  `json:"localRoot,omitempty"`
	AccessKeyID      string                  `json:"accessKeyId,omitempty"`
	AccessKeySecret  string                  `json:"accessKeySecret,omitempty"`
	CredentialSource StorageCredentialSource `json:"credentialSource"`
	Used             bool                    `json:"used"`
	CreatedAt        time.Time               `json:"createdAt"`
	UpdatedAt        time.Time               `json:"updatedAt"`
}

type StorageProfileSet struct {
	ActiveProfileID string           `json:"activeProfileId"`
	Profiles        []StorageProfile `json:"profiles"`
}

type runtimeConfig struct {
	installationPath          string
	loadedRevision            uint64
	storageEnvironmentManaged bool
	storageProfiles           StorageProfileSet
	fileUploadPolicy          FileUploadPolicy
}

type StorageRuntime struct {
	InstallationPath   string
	LoadedRevision     uint64
	EnvironmentManaged bool
	ActiveProfileID    string
	Profiles           []StorageProfile
}

func (cfg Config) StorageRuntime() StorageRuntime {
	profiles := cloneStorageProfiles(cfg.runtime.storageProfiles.Profiles)
	active := cfg.runtime.storageProfiles.ActiveProfileID
	if len(profiles) == 0 {
		profile := SynthesizeStorageProfile(cfg.Storage)
		profiles = []StorageProfile{profile}
		active = profile.ID
	}
	return StorageRuntime{
		InstallationPath:   cfg.runtime.installationPath,
		LoadedRevision:     cfg.runtime.loadedRevision,
		EnvironmentManaged: cfg.runtime.storageEnvironmentManaged,
		ActiveProfileID:    active,
		Profiles:           profiles,
	}
}

func (cfg *Config) setStorageRuntime(
	path string,
	revision uint64,
	profiles StorageProfileSet,
) {
	cfg.runtime.installationPath = path
	cfg.runtime.loadedRevision = revision
	cfg.runtime.storageProfiles = StorageProfileSet{
		ActiveProfileID: profiles.ActiveProfileID,
		Profiles:        cloneStorageProfiles(profiles.Profiles),
	}
}

func cloneStorageProfiles(input []StorageProfile) []StorageProfile {
	return append([]StorageProfile(nil), input...)
}

func SynthesizeStorageProfile(storage Storage) StorageProfile {
	now := time.Now().UTC()
	if strings.TrimSpace(storage.Driver) == "" {
		storage.Driver = "local"
	}
	if strings.EqualFold(storage.Driver, "local") && strings.TrimSpace(storage.LocalRoot) == "" {
		storage.LocalRoot = "data/uploads"
	}
	provider := storage.Provider
	if provider == "" {
		provider = inferStorageProvider(storage)
	}
	profile := StorageProfile{
		Name:             defaultStorageProfileName(provider),
		Provider:         provider,
		Status:           StorageProfileAvailable,
		Bucket:           strings.TrimSpace(storage.Bucket),
		Region:           strings.TrimSpace(storage.Region),
		Endpoint:         strings.TrimSpace(storage.Endpoint),
		AccessBaseURL:    strings.TrimRight(strings.TrimSpace(storage.AccessBaseURL), "/"),
		LocalRoot:        strings.TrimSpace(storage.LocalRoot),
		AccessKeyID:      storage.AccessKeyID,
		AccessKeySecret:  storage.AccessKeySecret,
		CredentialSource: StorageCredentialsManaged,
		Used:             true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if provider == StorageProviderLocal {
		profile.CredentialSource = StorageCredentialsNone
	}
	if profile.AccessKeyID == "" && provider != StorageProviderLocal {
		profile.CredentialSource = StorageCredentialsEnvironment
	}
	if provider == StorageProviderR2 {
		profile.AccountID = r2AccountID(profile.Endpoint)
	}
	profile.ID = deterministicStorageProfileID(profile)
	return profile
}

func inferStorageProvider(storage Storage) StorageProvider {
	switch strings.ToLower(strings.TrimSpace(storage.Driver)) {
	case "oss":
		return StorageProviderAlibabaOSS
	case "s3":
		host := ""
		if parsed, err := url.Parse(strings.TrimSpace(storage.Endpoint)); err == nil {
			host = strings.ToLower(parsed.Hostname())
		}
		if strings.HasSuffix(host, ".r2.cloudflarestorage.com") {
			return StorageProviderR2
		}
		if storage.Endpoint != "" {
			return StorageProviderMinIO
		}
		return StorageProviderAWSS3
	default:
		return StorageProviderLocal
	}
}

func defaultStorageProfileName(provider StorageProvider) string {
	switch provider {
	case StorageProviderAlibabaOSS:
		return "Alibaba Cloud OSS"
	case StorageProviderAWSS3:
		return "AWS S3"
	case StorageProviderMinIO:
		return "MinIO"
	case StorageProviderR2:
		return "Cloudflare R2"
	default:
		return "Local storage"
	}
}

func deterministicStorageProfileID(profile StorageProfile) string {
	canonical := strings.Join([]string{
		string(profile.Provider), profile.Bucket, profile.Region,
		profile.Endpoint, profile.AccessBaseURL, profile.AccountID, profile.LocalRoot,
	}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	raw := append([]byte(nil), sum[:16]...)
	raw[6] = (raw[6] & 0x0f) | 0x50
	raw[8] = (raw[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(raw)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexValue[:8], hexValue[8:12], hexValue[12:16], hexValue[16:20], hexValue[20:])
}

func ValidateStorageProfileSet(set StorageProfileSet) error {
	if len(set.Profiles) == 0 {
		return errors.New("at least one storage profile is required")
	}
	seen := make(map[string]struct{}, len(set.Profiles))
	active := false
	for index := range set.Profiles {
		profile := set.Profiles[index]
		if err := ValidateStorageProfile(profile); err != nil {
			return fmt.Errorf("storage profile %d: %w", index, err)
		}
		if _, exists := seen[profile.ID]; exists {
			return errors.New("storage profile identifiers must be unique")
		}
		seen[profile.ID] = struct{}{}
		if profile.ID == set.ActiveProfileID {
			active = profile.Status == StorageProfileAvailable
		}
	}
	if !active {
		return errors.New("active storage profile must exist and be available")
	}
	return nil
}

func ValidateStorageProfile(profile StorageProfile) error {
	if !validUUID(profile.ID) || strings.TrimSpace(profile.Name) == "" || len(profile.Name) > 120 {
		return errors.New("storage profile identity is invalid")
	}
	if profile.Status != StorageProfileAvailable && profile.Status != StorageProfileArchived {
		return errors.New("storage profile status is invalid")
	}
	switch profile.Provider {
	case StorageProviderLocal:
		if strings.TrimSpace(profile.LocalRoot) == "" || profile.CredentialSource != StorageCredentialsNone {
			return errors.New("local storage profile is invalid")
		}
	case StorageProviderAlibabaOSS, StorageProviderAWSS3, StorageProviderMinIO, StorageProviderR2:
		if strings.TrimSpace(profile.Bucket) == "" || strings.TrimSpace(profile.Region) == "" {
			return errors.New("cloud storage bucket and region are required")
		}
		if profile.CredentialSource == StorageCredentialsManaged &&
			(profile.AccessKeyID == "" || profile.AccessKeySecret == "") {
			return errors.New("managed storage credentials are incomplete")
		}
		if profile.CredentialSource != StorageCredentialsManaged &&
			profile.CredentialSource != StorageCredentialsEnvironment {
			return errors.New("cloud storage credential source is invalid")
		}
		if profile.Provider == StorageProviderMinIO && strings.TrimSpace(profile.Endpoint) == "" {
			return errors.New("MinIO endpoint is required")
		}
		if profile.Provider == StorageProviderAlibabaOSS {
			if err := ValidateStorageAccessBaseURL(profile.AccessBaseURL); err != nil {
				return err
			}
		} else if strings.TrimSpace(profile.AccessBaseURL) != "" {
			return errors.New("storage access base URL is only valid for Alibaba Cloud OSS")
		}
		if profile.Provider == StorageProviderR2 && strings.TrimSpace(profile.AccountID) == "" {
			return errors.New("Cloudflare R2 account ID is required")
		}
	default:
		return errors.New("storage provider is invalid")
	}
	return nil
}

func (profile StorageProfile) StorageConfig() Storage {
	driver := "s3"
	switch profile.Provider {
	case StorageProviderLocal:
		driver = "local"
	case StorageProviderAlibabaOSS:
		driver = "oss"
	}
	endpoint := profile.Endpoint
	region := profile.Region
	forcePathStyle := profile.Provider == StorageProviderMinIO || profile.Provider == StorageProviderR2
	if profile.Provider == StorageProviderR2 {
		endpoint = "https://" + profile.AccountID + ".r2.cloudflarestorage.com"
		region = "auto"
	}
	return Storage{
		ProfileID:       profile.ID,
		Provider:        profile.Provider,
		Driver:          driver,
		LocalRoot:       profile.LocalRoot,
		Bucket:          profile.Bucket,
		Region:          region,
		Endpoint:        endpoint,
		AccessBaseURL:   profile.AccessBaseURL,
		AccessKeyID:     profile.AccessKeyID,
		AccessKeySecret: profile.AccessKeySecret,
		ForcePathStyle:  forcePathStyle,
	}
}

// ValidateStorageAccessBaseURL accepts the browser-visible HTTPS origin used
// to sign private OSS reads. It may be an official bucket endpoint or a bound
// custom CNAME, but never carries credentials or request-specific URL state.
func ValidateStorageAccessBaseURL(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return errors.New("OSS access base URL must be an HTTPS origin without credentials, path, query, or fragment")
	}
	return nil
}

func ActiveStorageProfile(set StorageProfileSet) (StorageProfile, bool) {
	for _, profile := range set.Profiles {
		if profile.ID == set.ActiveProfileID {
			return profile, true
		}
	}
	return StorageProfile{}, false
}

func SortedStorageProfiles(profiles []StorageProfile) []StorageProfile {
	result := cloneStorageProfiles(profiles)
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func r2AccountID(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	return strings.TrimSuffix(host, ".r2.cloudflarestorage.com")
}

// ValidateStorageEndpointPolicy applies the same SSRF boundary to legacy
// environment-managed S3-compatible endpoints as the profile API applies to
// newly submitted MinIO profiles.
func ValidateStorageEndpointPolicy(environment string, storage Storage) error {
	endpoint := strings.TrimSpace(storage.Endpoint)
	if endpoint == "" {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("storage endpoint must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	ip := net.ParseIP(host)
	if host == "169.254.169.254" || host == "100.100.100.200" ||
		host == "metadata.google.internal" || host == "instance-data.ec2.internal" ||
		host == "fd00:ec2::254" || (ip != nil && ip.IsLinkLocalUnicast()) {
		return errors.New("storage endpoint targets a forbidden metadata or link-local address")
	}
	if storage.Driver == "oss" {
		if parsed.Scheme != "https" || !strings.HasSuffix(host, ".aliyuncs.com") {
			return errors.New("OSS endpoint must be an official HTTPS aliyuncs.com endpoint")
		}
		return nil
	}
	if environment != "production" || strings.HasSuffix(host, ".r2.cloudflarestorage.com") {
		return nil
	}
	if parsed.Scheme != "https" {
		return errors.New("production S3-compatible endpoints must use HTTPS")
	}
	for _, candidate := range storage.EndpointAllowlist {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if host == candidate || (strings.HasPrefix(candidate, "*.") && strings.HasSuffix(host, candidate[1:])) {
			return nil
		}
	}
	return errors.New("production S3-compatible endpoint host is not in AGINEX_STORAGE_ENDPOINT_ALLOWLIST")
}
