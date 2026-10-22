package portal

import (
	"bytes"
	"crypto/subtle"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Service) getFaceLink(w http.ResponseWriter, r *http.Request) {
	if _, e := s.store.GetAccount(r.Context(), r.PathValue("id")); e != nil {
		fail(w, 404, "账号不存在")
		return
	}
	var cipher []byte
	var expires time.Time
	e := s.db.QueryRow(r.Context(), `SELECT token_cipher,expires_at FROM portal_face_links WHERE account_id=$1 AND expires_at>now()`, r.PathValue("id")).Scan(&cipher, &expires)
	if e == pgx.ErrNoRows {
		jsonReply(w, 200, map[string]any{"url": nil})
		return
	}
	if e != nil {
		s.dbError(w, e)
		return
	}
	plain, e := s.vault.Decrypt(cipher, "face-link:"+r.PathValue("id"))
	if e != nil {
		s.dbError(w, e)
		return
	}
	jsonReply(w, 200, map[string]any{"url": "/face-upload/" + r.PathValue("id") + "#token=" + string(plain), "expires_at": expires})
}
func (s *Service) rotateFaceLink(w http.ResponseWriter, r *http.Request) {
	if _, e := s.store.GetAccount(r.Context(), r.PathValue("id")); e != nil {
		fail(w, 404, "账号不存在")
		return
	}
	var in struct {
		Days int `json:"days"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if in.Days == 0 {
		in.Days = 30
	}
	if in.Days < 1 || in.Days > 365 {
		fail(w, 400, "链接有效期为 1–365 天")
		return
	}
	secret, e := token()
	if e != nil {
		s.dbError(w, e)
		return
	}
	cipher, e := s.vault.Encrypt([]byte(secret), "face-link:"+r.PathValue("id"))
	if e != nil {
		s.dbError(w, e)
		return
	}
	expires := time.Now().Add(time.Duration(in.Days) * 24 * time.Hour)
	_, e = s.db.Exec(r.Context(), `INSERT INTO portal_face_links(account_id,token_hash,token_cipher,expires_at)VALUES($1,$2,$3,$4)ON CONFLICT(account_id)DO UPDATE SET token_hash=EXCLUDED.token_hash,token_cipher=EXCLUDED.token_cipher,expires_at=EXCLUDED.expires_at,updated_at=now()`, r.PathValue("id"), digest(secret), cipher, expires)
	if e != nil {
		s.dbError(w, e)
		return
	}
	s.audit(r.Context(), "face-link.rotate", r.PathValue("id"))
	jsonReply(w, 201, map[string]any{"url": "/face-upload/" + r.PathValue("id") + "#token=" + secret, "expires_at": expires})
}
func (s *Service) revokeFaceLink(w http.ResponseWriter, r *http.Request) {
	if _, e := s.store.GetAccount(r.Context(), r.PathValue("id")); e != nil {
		fail(w, 404, "账号不存在")
		return
	}
	_, e := s.db.Exec(r.Context(), `DELETE FROM portal_face_links WHERE account_id=$1`, r.PathValue("id"))
	if e != nil {
		s.dbError(w, e)
		return
	}
	s.audit(r.Context(), "face-link.revoke", r.PathValue("id"))
	jsonReply(w, 200, map[string]bool{"ok": true})
}
func faceToken(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}
func checkFaceToken(hash, secret string) bool {
	return len(secret) == 43 && subtle.ConstantTimeCompare([]byte(hash), []byte(digest(secret))) == 1
}
func (s *Service) publicFaceInfo(w http.ResponseWriter, r *http.Request) {
	var hash string
	e := s.db.QueryRow(r.Context(), `SELECT l.token_hash FROM portal_face_links l JOIN runner_accounts a ON a.id=l.account_id JOIN portal_users u ON u.id=a.owner_user_id WHERE l.account_id=$1 AND l.expires_at>now() AND u.enabled`, r.PathValue("id")).Scan(&hash)
	if e != nil || !checkFaceToken(hash, faceToken(r)) {
		fail(w, 404, "链接无效或已过期，请向账号管理者申请新链接")
		return
	}
	jsonReply(w, 200, map[string]string{"account_id": r.PathValue("id"), "message": "仅更新该账号的自定义人脸照片；请勿泄露本链接，持有链接的人可以修改照片。"})
}
func cleanPortrait(data []byte) ([]byte, error) {
	config, _, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil || config.Width < 32 || config.Height < 32 || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 16_000_000 {
		return nil, fmt.Errorf("请使用 32–8192 像素且不超过 1600 万像素的 JPEG/PNG 图片")
	}
	img, _, e := image.Decode(bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	var out bytes.Buffer
	if e = jpeg.Encode(&out, img, &jpeg.Options{Quality: 92}); e != nil {
		return nil, e
	}
	if out.Len() > 6<<20 {
		return nil, fmt.Errorf("照片处理后超过 6 MB")
	}
	return out.Bytes(), nil
}
func (s *Service) publicFaceUpload(w http.ResponseWriter, r *http.Request) {
	if s.Faces == nil {
		fail(w, 503, "人脸服务未启动")
		return
	}
	if !s.allow("face-upload:"+remoteIP(r), 15, 10*time.Minute) {
		fail(w, 429, "上传过于频繁")
		return
	}
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	var hash string
	e = tx.QueryRow(r.Context(), `SELECT l.token_hash FROM portal_face_links l JOIN runner_accounts a ON a.id=l.account_id JOIN portal_users u ON u.id=a.owner_user_id WHERE l.account_id=$1 AND l.expires_at>now() AND u.enabled FOR SHARE OF l`, r.PathValue("id")).Scan(&hash)
	if e != nil || !checkFaceToken(hash, faceToken(r)) {
		fail(w, 404, "链接无效或已过期")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
	data, e := io.ReadAll(r.Body)
	if e != nil {
		fail(w, 400, "图片超过 6 MB")
		return
	}
	data, e = cleanPortrait(data)
	if e != nil {
		fail(w, 400, e.Error())
		return
	}
	if _, e = s.Faces.SetCustomFace(r.Context(), r.PathValue("id"), "image/jpeg", data); e != nil {
		fail(w, 400, "照片更新失败，请稍后重试")
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		s.dbError(w, e)
		return
	}
	s.audit(r.Context(), "face.public-update", r.PathValue("id"))
	jsonReply(w, 200, map[string]bool{"ok": true})
}
