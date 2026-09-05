package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	frameworkaudit "github.com/xgtian-root/aginex/backend/framework/audit"
	"github.com/xgtian-root/aginex/backend/framework/httpx"
	"github.com/xgtian-root/aginex/backend/internal/config"
	"github.com/xgtian-root/aginex/backend/internal/domain"
	"github.com/xgtian-root/aginex/backend/internal/platform/storage"
	"gorm.io/gorm"
)

const storageReadinessTimeout = 3 * time.Second

var r2AccountPattern = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

type storageDocument struct {
	set              config.StorageProfileSet
	fileUploadPolicy config.FileUploadPolicy
	revision         uint64
	updated          time.Time
}

func (a *App) currentStorageDocument() (storageDocument, error) {
	runtime := a.cfg.StorageRuntime()
	uploadRuntime := a.cfg.FileUploadRuntime()
	if strings.TrimSpace(runtime.InstallationPath) == "" {
		return storageDocument{
			set: config.StorageProfileSet{
				ActiveProfileID: runtime.ActiveProfileID,
				Profiles:        runtime.Profiles,
			},
			fileUploadPolicy: config.FileUploadPolicy{
				MaxUploadBytes:          uploadRuntime.MaxUploadBytes,
				ResumableUploadsEnabled: uploadRuntime.ResumableUploadsEnabled,
			},
			revision: max(runtime.LoadedRevision, 1),
		}, nil
	}
	installation, err := config.ReadInstallation(runtime.InstallationPath)
	if err != nil {
		return storageDocument{}, err
	}
	if runtime.EnvironmentManaged {
		return storageDocument{
			set: config.StorageProfileSet{
				ActiveProfileID: runtime.ActiveProfileID,
				Profiles:        runtime.Profiles,
			},
			fileUploadPolicy: installation.FileUploadPolicy,
			revision:         installation.Revision,
			updated:          installation.UpdatedAt,
		}, nil
	}
	profiles := append([]config.StorageProfile(nil), installation.Profiles...)
	for index := range profiles {
		if runtimeProfile, ok := storageRuntimeProfile(runtime.Profiles, profiles[index].ID); ok && profiles[index].Provider == config.StorageProviderLocal {
			profiles[index].LocalRoot = runtimeProfile.LocalRoot
		}
	}
	return storageDocument{
		set: config.StorageProfileSet{
			ActiveProfileID: installation.ActiveProfileID,
			Profiles:        profiles,
		},
		fileUploadPolicy: installation.FileUploadPolicy,
		revision:         installation.Revision,
		updated:          installation.UpdatedAt,
	}, nil
}

func storageRuntimeProfile(profiles []config.StorageProfile, id string) (config.StorageProfile, bool) {
	for _, profile := range profiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return config.StorageProfile{}, false
}

func storageETag(revision uint64) string { return fmt.Sprintf(`"%d"`, revision) }

func expectedStorageRevision(c *gin.Context) (uint64, bool) {
	value := strings.TrimSpace(c.GetHeader("If-Match"))
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		writeProblem(c, http.StatusBadRequest, "Revision required", "Send the current quoted ETag in If-Match.")
		return 0, false
	}
	revision, err := strconv.ParseUint(value[1:len(value)-1], 10, 64)
	if err != nil || revision == 0 {
		writeProblem(c, http.StatusBadRequest, "Revision is invalid", "Refresh storage settings and retry with its ETag.")
		return 0, false
	}
	return revision, true
}

func (a *App) storageWritesAvailable(c *gin.Context) bool {
	runtime := a.cfg.StorageRuntime()
	if runtime.EnvironmentManaged {
		writeProblem(c, http.StatusConflict, "Storage is environment managed", "Remove AGINEX_STORAGE_DRIVER to manage profiles from the console.")
		return false
	}
	if strings.TrimSpace(runtime.InstallationPath) == "" {
		writeProblem(c, http.StatusConflict, "Storage settings are unavailable", "This runtime has no writable installation configuration.")
		return false
	}
	return true
}

func (a *App) unboundFileCount(ctx context.Context) (int64, error) {
	if !registryHasResource(a.registry, filesResource) {
		return 0, nil
	}
	var count int64
	err := a.db.WithContext(ctx).Model(&domain.FileObject{}).
		Where("storage_profile_id IS NULL").Count(&count).Error
	return count, err
}

func (a *App) profileUseCount(ctx context.Context, id string) (int64, error) {
	if !registryHasResource(a.registry, filesResource) {
		return 0, nil
	}
	var count int64
	err := a.db.WithContext(ctx).Model(&domain.FileObject{}).
		Where("storage_profile_id = ?", id).Count(&count).Error
	if err != nil || a.cfg.Jobs.Driver != "postgres" {
		return count, err
	}
	var jobs int64
	err = a.db.WithContext(ctx).Table("aginex_jobs").
		Where("type = ? AND state <> ? AND payload ->> 'profileId' = ?", "storage.cleanup", "succeeded", id).
		Count(&jobs).Error
	return count + jobs, err
}

func (a *App) storageSettingsResponse(ctx context.Context, document storageDocument) (StorageSettingsResponse, error) {
	runtime := a.cfg.StorageRuntime()
	uploadRuntime := a.cfg.FileUploadRuntime()
	unbound, err := a.unboundFileCount(ctx)
	if err != nil {
		return StorageSettingsResponse{}, err
	}
	return StorageSettingsResponse{
		RuntimeActiveProfileID: runtime.ActiveProfileID,
		PendingActiveProfileID: document.set.ActiveProfileID,
		EnvironmentManaged:     runtime.EnvironmentManaged,
		RestartRequired: document.revision != max(runtime.LoadedRevision, 1) ||
			document.set.ActiveProfileID != runtime.ActiveProfileID ||
			document.fileUploadPolicy.MaxUploadBytes != uploadRuntime.MaxUploadBytes ||
			document.fileUploadPolicy.ResumableUploadsEnabled != uploadRuntime.ResumableUploadsEnabled,
		Revision: document.revision, RuntimeRevision: max(runtime.LoadedRevision, 1), UnboundFileCount: unbound,
		RuntimeFileUploadPolicy: FileUploadPolicyResponse{
			MaxUploadBytes:          uploadRuntime.MaxUploadBytes,
			ResumableUploadsEnabled: uploadRuntime.ResumableUploadsEnabled,
		},
		PendingFileUploadPolicy: FileUploadPolicyResponse{
			MaxUploadBytes:          document.fileUploadPolicy.MaxUploadBytes,
			ResumableUploadsEnabled: document.fileUploadPolicy.ResumableUploadsEnabled,
		},
	}, nil
}

func (a *App) updateFileUploadPolicy(c *gin.Context) {
	expected, ok := expectedStorageRevision(c)
	if !ok {
		return
	}
	input, ok := validatedRequestDTO[FileUploadPolicyUpdateRequest](c)
	if !ok {
		return
	}
	policy := config.FileUploadPolicy{
		MaxUploadBytes:          input.MaxUploadBytes,
		ResumableUploadsEnabled: input.ResumableUploadsEnabled,
	}
	if err := config.ValidateFileUploadPolicy(policy); err != nil {
		httpx.WriteProblem(
			c,
			http.StatusUnprocessableEntity,
			"FILE_UPLOAD_POLICY_INVALID",
			"File upload policy is invalid",
			err.Error(),
		)
		return
	}
	runtime := a.cfg.FileUploadRuntime()
	if strings.TrimSpace(runtime.InstallationPath) == "" {
		writeProblem(
			c,
			http.StatusConflict,
			"File upload settings are unavailable",
			"This runtime has no writable installation configuration.",
		)
		return
	}
	var body StorageSettingsResponse
	var headers http.Header
	saved, err := config.CommitInstallationFileUploadPolicy(
		runtime.InstallationPath,
		expected,
		policy,
		func(saved config.Installation) error {
			document, documentErr := a.currentStorageDocumentFromInstallation(saved)
			if documentErr != nil {
				return documentErr
			}
			body, documentErr = a.storageSettingsResponse(c.Request.Context(), document)
			if documentErr != nil {
				return documentErr
			}
			headers = make(http.Header)
			headers.Set("ETag", storageETag(saved.Revision))
			principal := currentPrincipal(c)
			return a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
				if err := a.completeIdempotentWrite(c, tx, http.StatusOK, body, headers); err != nil {
					return frameworkaudit.Event{}, err
				}
				return successfulAuditEvent(
					c,
					&principal.User.ID,
					"storage-settings:update-file-upload-policy",
					"storage-settings",
					"file-upload-policy",
					"Updated file upload policy",
					map[string]any{"revision": expected},
					map[string]any{
						"revision":                saved.Revision,
						"maxUploadBytes":          policy.MaxUploadBytes,
						"resumableUploadsEnabled": policy.ResumableUploadsEnabled,
					},
				), nil
			})
		},
	)
	if err != nil {
		if errors.Is(err, config.ErrInstallationRevisionConflict) {
			writeProblem(c, http.StatusConflict, "Storage settings changed", "Refresh storage settings before retrying.")
			return
		}
		logRequestFailure(c, "commit_file_upload_policy", err)
		writeProblem(c, http.StatusInternalServerError, "File upload settings could not be committed", "The private installation configuration and required audit record were not committed.")
		return
	}
	c.Header("ETag", storageETag(saved.Revision))
	c.JSON(http.StatusOK, body)
}

func (a *App) currentStorageDocumentFromInstallation(
	installation config.Installation,
) (storageDocument, error) {
	runtime := a.cfg.StorageRuntime()
	if runtime.EnvironmentManaged {
		return storageDocument{
			set: config.StorageProfileSet{
				ActiveProfileID: runtime.ActiveProfileID,
				Profiles:        runtime.Profiles,
			},
			fileUploadPolicy: installation.FileUploadPolicy,
			revision:         installation.Revision,
			updated:          installation.UpdatedAt,
		}, nil
	}
	profiles := append([]config.StorageProfile(nil), installation.Profiles...)
	for index := range profiles {
		if runtimeProfile, ok := storageRuntimeProfile(runtime.Profiles, profiles[index].ID); ok && profiles[index].Provider == config.StorageProviderLocal {
			profiles[index].LocalRoot = runtimeProfile.LocalRoot
		}
	}
	return storageDocument{
		set: config.StorageProfileSet{
			ActiveProfileID: installation.ActiveProfileID,
			Profiles:        profiles,
		},
		fileUploadPolicy: installation.FileUploadPolicy,
		revision:         installation.Revision,
		updated:          installation.UpdatedAt,
	}, nil
}

func (a *App) getStorageSettings(c *gin.Context) {
	document, err := a.currentStorageDocument()
	if err != nil {
		logRequestFailure(c, "read_storage_settings", err)
		writeProblem(c, http.StatusInternalServerError, "Storage settings unavailable", "The storage configuration could not be read.")
		return
	}
	response, err := a.storageSettingsResponse(c.Request.Context(), document)
	if err != nil {
		logRequestFailure(c, "count_unbound_files", err)
		writeProblem(c, http.StatusInternalServerError, "Storage settings unavailable", "File storage state could not be read.")
		return
	}
	c.Header("ETag", storageETag(document.revision))
	c.JSON(http.StatusOK, response)
}

func (a *App) listStorageProfiles(c *gin.Context) {
	document, err := a.currentStorageDocument()
	if err != nil {
		writeStorageReadFailure(c, err)
		return
	}
	profiles := config.SortedStorageProfiles(document.set.Profiles)
	page, pageSize := pagination(c)
	start := (page - 1) * pageSize
	if start > len(profiles) {
		start = len(profiles)
	}
	end := min(start+pageSize, len(profiles))
	items := make([]StorageProfileResponse, 0, end-start)
	for _, profile := range profiles[start:end] {
		items = append(items, a.storageProfileResponse(c.Request.Context(), document, profile))
	}
	c.Header("ETag", storageETag(document.revision))
	c.JSON(http.StatusOK, Page[StorageProfileResponse]{Items: items, Page: page, PageSize: pageSize, Total: int64(len(profiles))})
}

func (a *App) getStorageProfile(c *gin.Context) {
	document, err := a.currentStorageDocument()
	if err != nil {
		writeStorageReadFailure(c, err)
		return
	}
	profile, _, ok := findStorageProfile(document.set, c.Param("id"))
	if !ok {
		writeProblem(c, http.StatusNotFound, "Storage profile not found", "No storage profile matches this identifier.")
		return
	}
	c.Header("ETag", storageETag(document.revision))
	c.JSON(http.StatusOK, a.storageProfileResponse(c.Request.Context(), document, profile))
}

func writeStorageReadFailure(c *gin.Context, err error) {
	logRequestFailure(c, "read_storage_profiles", err)
	writeProblem(c, http.StatusInternalServerError, "Storage profiles unavailable", "The storage configuration could not be read.")
}

func (a *App) storageProfileResponse(ctx context.Context, document storageDocument, profile config.StorageProfile) StorageProfileResponse {
	used, err := a.profileUseCount(ctx, profile.ID)
	if err != nil {
		used = 1
	}
	effective := profile.StorageConfig()
	runtime := a.cfg.StorageRuntime()
	return StorageProfileResponse{
		ID: profile.ID, Name: profile.Name, Provider: string(profile.Provider), Driver: effective.Driver,
		Status: string(profile.Status), Bucket: profile.Bucket, Region: effective.Region,
		Endpoint: effective.Endpoint, AccountID: profile.AccountID, LocalRoot: profile.LocalRoot,
		AuthConfigured: profile.Provider == config.StorageProviderLocal || (profile.AccessKeyID != "" && profile.AccessKeySecret != "") || profile.CredentialSource == config.StorageCredentialsEnvironment,
		Used:           profile.Used || used > 0, Active: runtime.ActiveProfileID == profile.ID,
		PendingActive: document.set.ActiveProfileID == profile.ID, CreatedAt: profile.CreatedAt, UpdatedAt: profile.UpdatedAt,
	}
}

func findStorageProfile(set config.StorageProfileSet, id string) (config.StorageProfile, int, bool) {
	for index, profile := range set.Profiles {
		if profile.ID == id {
			return profile, index, true
		}
	}
	return config.StorageProfile{}, -1, false
}

func (a *App) createStorageProfile(c *gin.Context) {
	if !a.storageWritesAvailable(c) {
		return
	}
	expected, ok := expectedStorageRevision(c)
	if !ok {
		return
	}
	input, ok := validatedRequestDTO[StorageProfileRequest](c)
	if !ok {
		return
	}
	profile, err := a.profileFromRequest(input, nil)
	if err != nil {
		writeStorageValidationProblem(c, err)
		return
	}
	if err := a.checkStorageReadiness(c.Request.Context(), profile); err != nil {
		writeStorageReadinessProblem(c, err)
		return
	}
	document, err := a.currentStorageDocument()
	if err != nil {
		writeStorageReadFailure(c, err)
		return
	}
	document.set.Profiles = append(document.set.Profiles, profile)
	a.commitStorageProfiles(c, expected, document.set, "storage-profiles:create", profile.ID, profile.Provider,
		map[string]any{"created": true}, http.StatusCreated,
		func(saved storageDocument) any { return a.storageProfileResponse(c.Request.Context(), saved, profile) })
}

func (a *App) updateStorageProfile(c *gin.Context) {
	if !a.storageWritesAvailable(c) {
		return
	}
	expected, ok := expectedStorageRevision(c)
	if !ok {
		return
	}
	input, ok := validatedRequestDTO[StorageProfileRequest](c)
	if !ok {
		return
	}
	document, err := a.currentStorageDocument()
	if err != nil {
		writeStorageReadFailure(c, err)
		return
	}
	current, index, found := findStorageProfile(document.set, c.Param("id"))
	if !found {
		writeProblem(c, http.StatusNotFound, "Storage profile not found", "No storage profile matches this identifier.")
		return
	}
	if current.Provider == config.StorageProviderLocal {
		writeProblem(c, http.StatusConflict, "Local profile is read-only", "Local storage is generated from AGINEX_STORAGE_LOCAL_ROOT.")
		return
	}
	next, err := a.profileFromRequest(input, &current)
	if err != nil {
		writeStorageValidationProblem(c, err)
		return
	}
	used, err := a.profileUseCount(c.Request.Context(), current.ID)
	if err != nil {
		writeStorageReadFailure(c, err)
		return
	}
	identityChanged := storageIdentity(current) != storageIdentity(next)
	if (used > 0 || current.Used) && identityChanged {
		writeProblem(c, http.StatusConflict, "Storage target is immutable", "Create a new profile to change a target already used by files.")
		return
	}
	if err := a.checkStorageReadiness(c.Request.Context(), next); err != nil {
		writeStorageReadinessProblem(c, err)
		return
	}
	document.set.Profiles[index] = next
	a.commitStorageProfiles(c, expected, document.set, "storage-profiles:update", next.ID, next.Provider,
		map[string]any{"nameChanged": current.Name != next.Name, "targetChanged": identityChanged, "authChanged": current.AccessKeyID != next.AccessKeyID || current.AccessKeySecret != next.AccessKeySecret}, http.StatusOK,
		func(saved storageDocument) any { return a.storageProfileResponse(c.Request.Context(), saved, next) })
}

func (a *App) deleteStorageProfile(c *gin.Context) {
	if !a.storageWritesAvailable(c) {
		return
	}
	expected, ok := expectedStorageRevision(c)
	if !ok {
		return
	}
	document, err := a.currentStorageDocument()
	if err != nil {
		writeStorageReadFailure(c, err)
		return
	}
	profile, index, found := findStorageProfile(document.set, c.Param("id"))
	if !found {
		writeProblem(c, http.StatusNotFound, "Storage profile not found", "No storage profile matches this identifier.")
		return
	}
	used, err := a.profileUseCount(c.Request.Context(), profile.ID)
	if err != nil {
		writeStorageReadFailure(c, err)
		return
	}
	if profile.Provider == config.StorageProviderLocal || profile.ID == document.set.ActiveProfileID || used > 0 || profile.Used {
		writeProblem(c, http.StatusConflict, "Storage profile cannot be deleted", "Only unused, non-default cloud profiles can be deleted.")
		return
	}
	document.set.Profiles = append(document.set.Profiles[:index], document.set.Profiles[index+1:]...)
	a.commitStorageProfiles(c, expected, document.set, "storage-profiles:delete", profile.ID, profile.Provider,
		map[string]any{"deleted": true}, http.StatusNoContent, func(storageDocument) any { return nil })
}

func (a *App) activateStorageProfile(c *gin.Context) { a.transitionStorageProfile(c, "activate") }
func (a *App) archiveStorageProfile(c *gin.Context)  { a.transitionStorageProfile(c, "archive") }
func (a *App) restoreStorageProfile(c *gin.Context)  { a.transitionStorageProfile(c, "restore") }

func (a *App) transitionStorageProfile(c *gin.Context, transition string) {
	if !a.storageWritesAvailable(c) {
		return
	}
	expected, ok := expectedStorageRevision(c)
	if !ok {
		return
	}
	document, err := a.currentStorageDocument()
	if err != nil {
		writeStorageReadFailure(c, err)
		return
	}
	profile, index, found := findStorageProfile(document.set, c.Param("id"))
	if !found {
		writeProblem(c, http.StatusNotFound, "Storage profile not found", "No storage profile matches this identifier.")
		return
	}
	action := "storage-profiles:" + transition
	changes := map[string]any{}
	status := http.StatusOK
	var response func(storageDocument) any
	switch transition {
	case "activate":
		if profile.Status != config.StorageProfileAvailable {
			writeProblem(c, http.StatusConflict, "Archived profile cannot be activated", "Restore the profile before making it default.")
			return
		}
		if err := a.checkStorageReadiness(c.Request.Context(), profile); err != nil {
			writeStorageReadinessProblem(c, err)
			return
		}
		document.set.ActiveProfileID = profile.ID
		changes["activeChanged"] = true
		response = func(saved storageDocument) any {
			value, _ := a.storageSettingsResponse(c.Request.Context(), saved)
			return value
		}
	case "archive":
		if profile.Provider == config.StorageProviderLocal {
			writeProblem(c, http.StatusConflict, "Local profile is read-only", "The system-generated local profile cannot be archived.")
			return
		}
		if profile.ID == document.set.ActiveProfileID {
			writeProblem(c, http.StatusConflict, "Default profile cannot be archived", "Activate another profile first.")
			return
		}
		if profile.Status != config.StorageProfileAvailable {
			writeProblem(c, http.StatusConflict, "Storage profile is already archived", "Restore the profile before archiving it again.")
			return
		}
		profile.Status = config.StorageProfileArchived
		profile.UpdatedAt = time.Now().UTC()
		document.set.Profiles[index] = profile
		changes["archived"] = true
		response = func(saved storageDocument) any { return a.storageProfileResponse(c.Request.Context(), saved, profile) }
	case "restore":
		if profile.Provider == config.StorageProviderLocal {
			writeProblem(c, http.StatusConflict, "Local profile is read-only", "The system-generated local profile cannot be restored.")
			return
		}
		if profile.Status != config.StorageProfileArchived {
			writeProblem(c, http.StatusConflict, "Storage profile is already available", "Only archived profiles can be restored.")
			return
		}
		if err := a.checkStorageReadiness(c.Request.Context(), profile); err != nil {
			writeStorageReadinessProblem(c, err)
			return
		}
		profile.Status = config.StorageProfileAvailable
		profile.UpdatedAt = time.Now().UTC()
		document.set.Profiles[index] = profile
		changes["restored"] = true
		response = func(saved storageDocument) any { return a.storageProfileResponse(c.Request.Context(), saved, profile) }
	default:
		return
	}
	a.commitStorageProfiles(c, expected, document.set, action, profile.ID, profile.Provider, changes, status, response)
}

func (a *App) testStorageProfile(c *gin.Context) {
	if !a.storageWritesAvailable(c) {
		return
	}
	input, ok := validatedRequestDTO[StorageProfileRequest](c)
	if !ok {
		return
	}
	var current *config.StorageProfile
	if input.ID != "" {
		document, err := a.currentStorageDocument()
		if err != nil {
			writeStorageReadFailure(c, err)
			return
		}
		profile, _, found := findStorageProfile(document.set, input.ID)
		if !found {
			writeProblem(c, http.StatusNotFound, "Storage profile not found", "No storage profile matches this identifier.")
			return
		}
		current = &profile
	}
	profile, err := a.profileFromRequest(input, current)
	if err != nil {
		writeStorageValidationProblem(c, err)
		return
	}
	if err := a.checkStorageReadiness(c.Request.Context(), profile); err != nil {
		writeStorageReadinessProblem(c, err)
		return
	}
	response := StorageProfileTestResponse{OK: true, Provider: string(profile.Provider)}
	principal := currentPrincipal(c)
	if err := a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
		if err := a.completeIdempotentWrite(c, tx, http.StatusOK, response, nil); err != nil {
			return frameworkaudit.Event{}, err
		}
		return successfulAuditEvent(c, &principal.User.ID, "storage-profiles:test", "storage-profile", profile.ID, "Tested storage profile connectivity", nil,
			map[string]any{"profileId": profile.ID, "provider": profile.Provider, "tested": true}), nil
	}); err != nil {
		logRequestFailure(c, "audit_storage_profile_test", err)
		writeProblem(c, http.StatusInternalServerError, "Storage test could not be recorded", "The connectivity result was not committed.")
		return
	}
	c.JSON(http.StatusOK, response)
}

func (a *App) commitStorageProfiles(c *gin.Context, expected uint64, set config.StorageProfileSet, action, profileID string, provider config.StorageProvider, changes map[string]any, status int, response func(storageDocument) any) {
	path := a.cfg.StorageRuntime().InstallationPath
	for index := range set.Profiles {
		used, countErr := a.profileUseCount(c.Request.Context(), set.Profiles[index].ID)
		if countErr != nil {
			writeStorageReadFailure(c, countErr)
			return
		}
		set.Profiles[index].Used = set.Profiles[index].Used || used > 0
	}
	var body any
	var headers http.Header
	saved, err := config.CommitInstallationStorage(path, expected, set, func(saved config.Installation) error {
		document := storageDocument{set: config.StorageProfileSet{ActiveProfileID: saved.ActiveProfileID, Profiles: saved.Profiles}, revision: saved.Revision, updated: saved.UpdatedAt}
		body = response(document)
		headers = make(http.Header)
		headers.Set("ETag", storageETag(saved.Revision))
		principal := currentPrincipal(c)
		after := map[string]any{"profileId": profileID, "provider": provider, "revision": saved.Revision}
		for key, value := range changes {
			after[key] = value
		}
		return a.writes.Run(c.Request.Context(), func(tx *gorm.DB) (frameworkaudit.Event, error) {
			if err := a.completeIdempotentWrite(c, tx, status, body, headers); err != nil {
				return frameworkaudit.Event{}, err
			}
			return successfulAuditEvent(c, &principal.User.ID, action, "storage-profile", profileID, "Updated storage profile configuration",
				map[string]any{"revision": expected}, after), nil
		})
	})
	if err != nil {
		if errors.Is(err, config.ErrInstallationRevisionConflict) {
			writeProblem(c, http.StatusConflict, "Storage settings changed", "Refresh storage settings before retrying.")
			return
		}
		logRequestFailure(c, "commit_storage_profiles", err)
		writeProblem(c, http.StatusInternalServerError, "Storage settings could not be committed", "The private installation configuration and required audit record were not committed.")
		return
	}
	c.Header("ETag", storageETag(saved.Revision))
	if status == http.StatusNoContent {
		c.Status(status)
	} else {
		c.JSON(status, body)
	}
}

func storageIdentity(profile config.StorageProfile) string {
	return strings.Join([]string{string(profile.Provider), profile.Bucket, profile.Region, profile.Endpoint, profile.AccountID, profile.LocalRoot}, "\x00")
}

func (a *App) profileFromRequest(input StorageProfileRequest, current *config.StorageProfile) (config.StorageProfile, error) {
	now := time.Now().UTC()
	profile := config.StorageProfile{ID: uuid.NewString(), Status: config.StorageProfileAvailable, CreatedAt: now, UpdatedAt: now, CredentialSource: config.StorageCredentialsManaged}
	if current != nil {
		profile = *current
		profile.UpdatedAt = now
	}
	profile.Name = strings.TrimSpace(input.Name)
	provider := config.StorageProvider(strings.TrimSpace(input.Provider))
	if current != nil && provider != current.Provider {
		return config.StorageProfile{}, errors.New("provider cannot be changed")
	}
	profile.Provider = provider
	profile.Bucket = strings.TrimSpace(input.Bucket)
	profile.Region = strings.TrimSpace(input.Region)
	profile.Endpoint = strings.TrimRight(strings.TrimSpace(input.Endpoint), "/")
	profile.AccountID = strings.TrimSpace(input.AccountID)
	if input.AccessKeyID == nil && input.AccessKeySecret == nil {
		if current == nil {
			return config.StorageProfile{}, errors.New("access key ID and secret are required")
		}
	} else if input.AccessKeyID == nil || input.AccessKeySecret == nil {
		return config.StorageProfile{}, errors.New("access key ID and secret must be submitted together")
	} else {
		profile.AccessKeyID = strings.TrimSpace(*input.AccessKeyID)
		profile.AccessKeySecret = strings.TrimSpace(*input.AccessKeySecret)
		if profile.AccessKeyID == "" || profile.AccessKeySecret == "" {
			return config.StorageProfile{}, errors.New("access key ID and secret cannot be empty")
		}
		profile.CredentialSource = config.StorageCredentialsManaged
	}
	switch profile.Provider {
	case config.StorageProviderAlibabaOSS:
		if profile.AccountID != "" {
			return config.StorageProfile{}, errors.New("account ID is only valid for Cloudflare R2")
		}
		if profile.Endpoint != "" {
			if err := validateOSSEndpoint(profile.Endpoint); err != nil {
				return config.StorageProfile{}, err
			}
		}
	case config.StorageProviderAWSS3:
		if profile.Endpoint != "" || profile.AccountID != "" {
			return config.StorageProfile{}, errors.New("AWS S3 endpoint is resolved by the SDK")
		}
	case config.StorageProviderMinIO:
		if profile.Region == "" {
			profile.Region = "us-east-1"
		}
		if profile.AccountID != "" {
			return config.StorageProfile{}, errors.New("account ID is only valid for Cloudflare R2")
		}
		if err := validateMinIOEndpoint(profile.Endpoint, a.cfg.Environment, a.cfg.Storage.EndpointAllowlist); err != nil {
			return config.StorageProfile{}, err
		}
	case config.StorageProviderR2:
		if !r2AccountPattern.MatchString(profile.AccountID) {
			return config.StorageProfile{}, errors.New("Cloudflare R2 account ID is invalid")
		}
		if profile.Endpoint != "" {
			return config.StorageProfile{}, errors.New("Cloudflare R2 endpoint is generated by the server")
		}
		profile.Region = "auto"
	default:
		return config.StorageProfile{}, errors.New("select a supported cloud storage provider")
	}
	if err := config.ValidateStorageProfile(profile); err != nil {
		return config.StorageProfile{}, err
	}
	return profile, nil
}

func validateOSSEndpoint(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" || !strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".aliyuncs.com") {
		return errors.New("OSS endpoint must be an official HTTPS aliyuncs.com endpoint")
	}
	return nil
}

func validateMinIOEndpoint(value, environment string, allowlist []string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("MinIO endpoint must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	ip := net.ParseIP(host)
	if host == "169.254.169.254" || host == "100.100.100.200" ||
		host == "metadata.google.internal" || host == "instance-data.ec2.internal" ||
		host == "fd00:ec2::254" || (ip != nil && ip.IsLinkLocalUnicast()) {
		return errors.New("MinIO endpoint targets a forbidden metadata or link-local address")
	}
	if environment == "production" {
		if parsed.Scheme != "https" {
			return errors.New("production MinIO endpoints must use HTTPS")
		}
		allowed := false
		for _, candidate := range allowlist {
			candidate = strings.ToLower(strings.TrimSpace(candidate))
			if host == candidate || (strings.HasPrefix(candidate, "*.") && strings.HasSuffix(host, candidate[1:])) {
				allowed = true
				break
			}
		}
		if !allowed {
			return errors.New("production MinIO endpoint host is not in AGINEX_STORAGE_ENDPOINT_ALLOWLIST")
		}
	}
	return nil
}

func (a *App) checkStorageReadiness(ctx context.Context, profile config.StorageProfile) error {
	candidate, err := storage.FromConfig(ctx, profile.StorageConfig(), a.cfg.HTTP.PublicURL)
	if err != nil {
		return err
	}
	checker, ok := candidate.(storage.ReadinessChecker)
	if !ok {
		return errors.New("storage provider does not support readiness checks")
	}
	checkCtx, cancel := context.WithTimeout(ctx, storageReadinessTimeout)
	defer cancel()
	return checker.CheckReadiness(checkCtx)
}

func writeStorageValidationProblem(c *gin.Context, err error) {
	logRequestFailure(c, "validate_storage_profile", err)
	writeProblem(c, http.StatusBadRequest, "Storage profile is invalid", "Check the provider fields and submit the access key pair together.")
}

func writeStorageReadinessProblem(c *gin.Context, err error) {
	logRequestFailure(c, "test_storage_profile", err)
	writeProblem(c, http.StatusBadRequest, "Storage connection failed", "The bucket could not be verified within three seconds.")
}
