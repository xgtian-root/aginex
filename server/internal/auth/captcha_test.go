package auth

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image/png"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/platform/database"
	"github.com/xgtian-root/aginex/server/internal/platform/migrate"
	"gorm.io/gorm"
)

func TestCaptchaRenderingAndEntropy(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 12; i++ {
		answer, data, err := generateCaptcha()
		if err != nil {
			t.Fatal(err)
		}
		if len(answer) != 4 || strings.Trim(answer, captchaAlphabet) != "" {
			t.Fatal("invalid answer alphabet")
		}
		if seen[data] {
			t.Fatal("repeated image")
		}
		seen[data] = true
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(data, "data:image/png;base64,"))
		if err != nil {
			t.Fatal(err)
		}
		image, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if image.Bounds().Dx() != 160 || image.Bounds().Dy() != 56 {
			t.Fatal("unexpected image bounds")
		}
		if bytes.Contains(raw, []byte(answer)) {
			t.Fatal("plaintext answer in PNG")
		}
	}
}

func TestCaptchaServiceExpiryCleanupAndRollback(t *testing.T) {
	db := openAuthDatabase(t)
	service := NewCaptchaService("test-secret")
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	service.generate = func() (string, string, error) { return "A2B3", "data:image/png;base64,test", nil }
	issue := func() CaptchaChallenge {
		t.Helper()
		var challenge CaptchaChallenge
		if err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			challenge, _, err = service.IssueTx(tx, "browser")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return challenge
	}
	challenge := issue()
	var record captchaRecord
	if err := db.First(&record, "id = ?", challenge.ID).Error; err != nil {
		t.Fatal(err)
	}
	if len(record.AnswerHash) != 64 || len(record.BindingHash) != 64 || record.AnswerHash == service.digest("browser", challenge.ID, "A2B3") {
		t.Fatal("digests not separated")
	}
	sentinel := errors.New("audit failed")
	err := db.Transaction(func(tx *gorm.DB) error {
		valid, err := service.ConsumeTx(tx, challenge.ID, "a2b3", "browser")
		if err != nil || !valid {
			t.Fatalf("consume: %t %v", valid, err)
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if err := db.First(&record, "id = ?", challenge.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.Consumed {
		t.Fatal("failed audit must roll back consumption")
	}
	now = now.Add(CaptchaTTL)
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, err := service.ConsumeTx(tx, challenge.ID, "A2B3", "browser")
		return err
	}); !errors.Is(err, ErrInvalidCaptcha) {
		t.Fatalf("expiry boundary: %v", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, removed, err := service.IssueTx(tx, "browser")
		if removed != 1 {
			t.Fatalf("removed %d expired challenges", removed)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	service.generate = func() (string, string, error) { return "", "", sentinel }
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, _, err := service.IssueTx(tx, "browser")
		return err
	}); !errors.Is(err, sentinel) {
		t.Fatal("generator failure did not propagate")
	}
}

func TestCaptchaConcurrentConsumptionAcrossConnections(t *testing.T) {
	db := openAuthDatabase(t)
	var filename string
	// Read the temporary SQLite filename to open an independent application connection.
	rows, err := db.Raw("PRAGMA database_list").Rows()
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var seq int
		var name string
		var file string
		if err := rows.Scan(&seq, &name, &file); err != nil {
			t.Fatal(err)
		}
		if name == "main" {
			filename = file
		}
	}
	rows.Close()
	other, err := database.Open(config.Database{Driver: "sqlite", DSN: filename})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := other.DB()
	defer sqlDB.Close()
	assertCaptchaConcurrentConsumption(t, db, other)
}

func assertCaptchaConcurrentConsumption(t *testing.T, first, second *gorm.DB) {
	t.Helper()
	service := NewCaptchaService("shared-test-secret")
	service.generate = func() (string, string, error) { return "A2B3", "data:image/png;base64,test", nil }
	var challenge CaptchaChallenge
	if err := first.Transaction(func(tx *gorm.DB) error { var err error; challenge, _, err = service.IssueTx(tx, "browser"); return err }); err != nil {
		t.Fatal(err)
	}
	other := NewCaptchaService("shared-test-secret")
	start := make(chan struct{})
	results := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			db := first
			if i%2 == 1 {
				db = second
			}
			results <- db.Transaction(func(tx *gorm.DB) error {
				valid, err := other.ConsumeTx(tx, challenge.ID, "a2b3", "browser")
				if err != nil {
					return err
				}
				if !valid {
					return errors.New("unexpected answer mismatch")
				}
				return nil
			})
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrInvalidCaptcha) {
			t.Errorf("consume failed: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent consumers = %d, want 1", successes)
	}
}

// Like the existing migration matrix, these DSNs must name disposable test
// databases. CI runs database packages serially (-p 1).
func TestCaptchaDatabaseMatrix(t *testing.T) {
	for _, driver := range []string{"postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			key := "AGINEX_TEST_POSTGRES_DSN"
			if driver == "mysql" {
				key = "AGINEX_TEST_MYSQL_DSN"
			}
			dsn := os.Getenv(key)
			if dsn == "" {
				t.Skip("integration DSN is not configured")
			}
			cfg := config.Database{Driver: driver, DSN: dsn}
			first, err := database.Open(cfg)
			if err != nil {
				t.Fatal(err)
			}
			sqlFirst, _ := first.DB()
			defer sqlFirst.Close()
			if err := migrate.Up(sqlFirst, driver); err != nil {
				t.Fatal(err)
			}
			second, err := database.Open(cfg)
			if err != nil {
				t.Fatal(err)
			}
			sqlSecond, _ := second.DB()
			defer sqlSecond.Close()
			assertCaptchaConcurrentConsumption(t, first, second)
			service := NewCaptchaService("expiry-test-secret")
			now := time.Now().UTC()
			service.now = func() time.Time { return now }
			var issued CaptchaChallenge
			if err := first.Transaction(func(tx *gorm.DB) error { var err error; issued, _, err = service.IssueTx(tx, "browser"); return err }); err != nil {
				t.Fatal(err)
			}
			now = now.Add(CaptchaTTL + time.Second)
			if err := second.Transaction(func(tx *gorm.DB) error { _, err := service.ConsumeTx(tx, issued.ID, "wrong", "browser"); return err }); !errors.Is(err, ErrInvalidCaptcha) {
				t.Fatalf("expired captcha: %v", err)
			}
		})
	}
}
