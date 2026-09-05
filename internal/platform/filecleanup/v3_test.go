package filecleanup

import (
	"encoding/json"
	"testing"

	"github.com/xgtian-root/aginex/framework/observability"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/platform/storage"
)

func TestHandleV3DeletesThroughExactProfileIdentity(t *testing.T) {
	db := openCleanupDatabase(t)
	root := t.TempDir()
	local, err := storage.NewLocal(root, "/upload", "/content", storage.DefaultImagePolicy())
	if err != nil {
		t.Fatal(err)
	}
	file := seedCleanupFile(t, db, local, "deleting")
	cfg := config.WithDefaults(config.Config{
		Environment: "test",
		HTTP:        config.HTTP{PublicURL: "http://aginex.test"},
		Storage:     config.Storage{Driver: "local", LocalRoot: root},
	})
	profile := config.SynthesizeStorageProfile(cfg.Storage)
	file.StorageProfileID = &profile.ID
	if err := db.Model(&file).Update("storage_profile_id", profile.ID).Error; err != nil {
		t.Fatal(err)
	}
	recorder, err := observability.NewRecorder(nil)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := storage.NewRegistry(t.Context(), cfg, recorder)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewWithRegistry(db, registry, Config{Provider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(PayloadV3{FileID: file.ID, ProfileID: profile.ID, Provider: "local", Bucket: "", ObjectKey: file.ObjectKey, Mode: ModeExplicitDelete})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.HandleV3(t.Context(), payload); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Stat(t.Context(), file.ObjectKey); err == nil {
		t.Fatal("object still exists after v3 cleanup")
	}
}
