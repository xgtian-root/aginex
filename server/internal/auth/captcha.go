package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"gorm.io/gorm"
)

const CaptchaTTL = 5 * time.Minute
const captchaAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

var ErrInvalidCaptcha = errors.New("invalid captcha")

type captchaRecord struct {
	ID          string `gorm:"primaryKey"`
	AnswerHash  string
	BindingHash string
	ExpiresAt   time.Time
	Consumed    bool
}

func (captchaRecord) TableName() string { return "login_captchas" }

type CaptchaChallenge struct {
	ID        string
	Image     string
	ExpiresAt time.Time
}

// CaptchaService has no runtime bypass. Tests in this package can inject a
// deterministic generator and clock without changing the HTTP contract.
type CaptchaService struct {
	secret   string
	generate func() (string, string, error)
	now      func() time.Time
}

func NewCaptchaService(secret string) *CaptchaService {
	return &CaptchaService{secret: secret, generate: generateCaptcha, now: time.Now}
}
func (s *CaptchaService) digest(purpose, id, value string) string {
	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write([]byte("aginex:login-captcha:" + purpose + "\x00" + id + "\x00" + value))
	return hex.EncodeToString(mac.Sum(nil))
}

// IssueTx groups expiry cleanup and issuance in the caller's audited transaction.
func (s *CaptchaService) IssueTx(tx *gorm.DB, binding string) (CaptchaChallenge, int64, error) {
	answer, dataURL, err := s.generate()
	if err != nil {
		return CaptchaChallenge{}, 0, err
	}
	now := s.now().UTC()
	record := captchaRecord{ID: uuid.NewString(), ExpiresAt: now.Add(CaptchaTTL)}
	record.AnswerHash = s.digest("answer", record.ID, answer)
	record.BindingHash = s.digest("browser", record.ID, binding)
	cleanup := tx.Where("expires_at <= ?", now).Delete(&captchaRecord{})
	if cleanup.Error != nil {
		return CaptchaChallenge{}, 0, cleanup.Error
	}
	if err := tx.Create(&record).Error; err != nil {
		return CaptchaChallenge{}, 0, err
	}
	return CaptchaChallenge{ID: record.ID, Image: dataURL, ExpiresAt: record.ExpiresAt}, cleanup.RowsAffected, nil
}

// ConsumeTx claims before reading, so concurrent callers cannot both validate
// the same challenge. A wrong answer still commits the claim. The caller must
// commit this transaction before checking credentials.
func (s *CaptchaService) ConsumeTx(tx *gorm.DB, id, answer, binding string) (bool, error) {
	claim := tx.Model(&captchaRecord{}).
		Where("id = ? AND binding_hash = ? AND expires_at > ? AND consumed = ?",
			id, s.digest("browser", id, binding), s.now().UTC(), false).
		Update("consumed", true)
	if claim.Error != nil {
		return false, claim.Error
	}
	if claim.RowsAffected != 1 {
		return false, ErrInvalidCaptcha
	}
	var record captchaRecord
	if err := tx.First(&record, "id = ?", id).Error; err != nil {
		return false, err
	}
	expected := s.digest("answer", id, strings.ToUpper(strings.TrimSpace(answer)))
	return hmac.Equal([]byte(record.AnswerHash), []byte(expected)), nil
}

func generateCaptcha() (string, string, error) {
	// Randomness is obtained before rendering, and failure never falls back to a
	// predictable source. The random image parameters carry no plaintext answer.
	var entropy [128]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", "", err
	}
	code := make([]byte, 4)
	for i := range code {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(captchaAlphabet))))
		if err != nil {
			return "", "", err
		}
		code[i] = captchaAlphabet[n.Int64()]
	}
	parsed, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return "", "", err
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: 32, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return "", "", err
	}
	defer face.Close()
	const width, height = 160, 56
	src := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(src, src.Bounds(), image.NewUniform(color.RGBA{246, 247, 240, 255}), image.Point{}, draw.Src)
	for i, ch := range code {
		ink := color.RGBA{uint8(25 + entropy[i]%65), uint8(30 + entropy[i+4]%60), uint8(45 + entropy[i+8]%65), 255}
		d := font.Drawer{Dst: src, Src: image.NewUniform(ink), Face: face,
			Dot: fixed.P(15+i*34+int(entropy[i+12]%5), 38+int(entropy[i+16]%8))}
		d.DrawString(string(ch))
	}
	out := image.NewRGBA(src.Bounds())
	draw.Draw(out, out.Bounds(), image.NewUniform(color.RGBA{246, 247, 240, 255}), image.Point{}, draw.Src)
	phase := float64(entropy[20]) / 40
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			sx := x + int(3*math.Sin(float64(y)/9+phase))
			sy := y + int(2*math.Sin(float64(x)/17+phase))
			if image.Pt(sx, sy).In(src.Bounds()) {
				out.Set(x, y, src.At(sx, sy))
			}
		}
	}
	for line := 0; line < 3; line++ {
		for x := 0; x < width; x++ {
			y := 12 + line*14 + int(5*math.Sin(float64(x)/19+float64(entropy[21+line])))
			out.Set(x, y, color.RGBA{110, 135, 145, 255})
		}
	}
	for i := 24; i+1 < len(entropy); i += 2 {
		out.Set(int(entropy[i])%width, int(entropy[i+1])%height, color.RGBA{130, 145, 150, 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return "", "", err
	}
	return string(code), "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
