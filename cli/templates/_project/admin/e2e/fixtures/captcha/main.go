// This executable belongs only to the browser test harness, never the API.
// It changes the answer of one freshly issued challenge in a disposable test
// installation. The actual CSRF, browser binding and single-use checks still run.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"time"

	_ "github.com/glebarez/go-sqlite"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "captcha fixture:", err)
		os.Exit(1)
	}
}
func run() error {
	if os.Getenv("AGINEX_ENV") != "test" {
		return errors.New("requires AGINEX_ENV=test and a disposable installation")
	}
	path := os.Getenv("AGINEX_CONFIG_FILE")
	if !filepath.IsAbs(path) {
		return errors.New("requires explicit absolute AGINEX_CONFIG_FILE")
	}
	var input struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return errors.New("invalid challenge input")
	}
	if len(input.ID) != 36 {
		return errors.New("invalid challenge ID")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return errors.New("test installation is unavailable")
	}
	var installation struct {
		Version       int    `json:"version"`
		SessionSecret string `json:"sessionSecret"`
		Database      struct {
			Source string `json:"source"`
			Driver string `json:"driver"`
			DSN    string `json:"dsn"`
		} `json:"database"`
	}
	if err := json.Unmarshal(raw, &installation); err != nil || installation.Version != 1 {
		return errors.New("invalid test installation")
	}
	secret := installation.SessionSecret
	if value := os.Getenv("AGINEX_SESSION_SECRET"); value != "" {
		secret = value
	}
	if secret == "" {
		return errors.New("missing test session secret")
	}
	dsn := installation.Database.DSN
	if installation.Database.Source == "environment" {
		dsn = os.Getenv("AGINEX_DATABASE_DSN")
	}
	driver := map[string]string{"sqlite": "sqlite", "postgres": "pgx", "mysql": "mysql"}[installation.Database.Driver]
	if driver == "" || dsn == "" {
		return errors.New("missing test database")
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return errors.New("cannot open test database")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("aginex:login-captcha:answer\x00" + input.ID + "\x00A2B3"))
	query := "UPDATE login_captchas SET answer_hash = ? WHERE id = ? AND consumed = FALSE AND expires_at > ?"
	if driver == "pgx" {
		query = "UPDATE login_captchas SET answer_hash = $1 WHERE id = $2 AND consumed = FALSE AND expires_at > $3"
	}
	result, err := db.ExecContext(ctx, query, hex.EncodeToString(mac.Sum(nil)), input.ID, time.Now().UTC())
	if err != nil {
		return errors.New("cannot update test challenge")
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return errors.New("expected one fresh test challenge")
	}
	small := image.NewRGBA(image.Rect(0, 0, 40, 14))
	draw.Draw(small, small.Bounds(), image.White, image.Point{}, draw.Src)
	drawer := font.Drawer{Dst: small, Src: image.NewUniform(color.Black), Face: basicfont.Face7x13, Dot: fixed.P(6, 11)}
	drawer.DrawString("A2B3")
	large := image.NewRGBA(image.Rect(0, 0, 160, 56))
	for y := 0; y < 56; y++ {
		for x := 0; x < 160; x++ {
			large.Set(x, y, small.At(x/4, y/4))
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, large); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())})
}
