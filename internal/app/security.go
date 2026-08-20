package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/namechenxinyu/lan-share/internal/platform"
)

type pairAttempt struct {
	Count int
	Reset time.Time
}

func bearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if c, err := r.Cookie("lanshare_token"); err == nil {
		return strings.TrimSpace(c.Value)
	}
	return ""
}

func (a *App) authorizeFileAccess(w http.ResponseWriter, r *http.Request) (string, bool) {
	if isLocalRequest(r) || !a.SecureMode() {
		return bearerToken(r), true
	}
	token := bearerToken(r)
	if _, ok := a.security.VerifyToken(token); !ok {
		w.Header().Set("WWW-Authenticate", `LANShare realm="pairing-required"`)
		http.Error(w, "pairing required", http.StatusUnauthorized)
		return "", false
	}
	return token, true
}

func (a *App) allowPairAttempt(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	now := time.Now()
	a.attemptsMu.Lock()
	defer a.attemptsMu.Unlock()
	v := a.attempts[host]
	if v == nil || now.After(v.Reset) {
		a.attempts[host] = &pairAttempt{Count: 1, Reset: now.Add(time.Minute)}
		return true
	}
	v.Count++
	return v.Count <= 6
}

func pairingProof(code string, pub []byte, deviceID string) []byte {
	m := hmac.New(sha256.New, []byte(code))
	_, _ = m.Write(pub)
	_, _ = m.Write([]byte{0})
	_, _ = m.Write([]byte(deviceID))
	return m.Sum(nil)
}

func pairingKey(shared []byte, code, clientID, serverID string) [32]byte {
	h := sha256.New()
	_, _ = h.Write(shared)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(code))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(clientID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(serverID))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func sealPairToken(key [32]byte, token string) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return gcm.Seal(nil, nonce, []byte(token), nil), nonce, nil
}

func openPairToken(key [32]byte, nonce, ciphertext []byte) (string, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(nonce) != gcm.NonceSize() {
		return "", fmt.Errorf("invalid pair nonce")
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	return string(plain), err
}

func (a *App) handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !a.allowPairAttempt(r) {
		http.Error(w, "too many pairing attempts", http.StatusTooManyRequests)
		return
	}
	var body struct {
		DeviceID string `json:"device_id"`
		Name     string `json:"name"`
		PubKey   string `json:"pubkey"`
		Proof    string `json:"proof"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	clientPubRaw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(body.PubKey))
	if err != nil {
		http.Error(w, "invalid pairing public key", 400)
		return
	}
	proof, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(body.Proof))
	if err != nil {
		http.Error(w, "invalid pairing proof", 400)
		return
	}
	code := a.security.PairCode()
	expected := pairingProof(code, clientPubRaw, strings.TrimSpace(body.DeviceID))
	if len(proof) != len(expected) || subtle.ConstantTimeCompare(proof, expected) != 1 {
		http.Error(w, "invalid pairing code", http.StatusUnauthorized)
		return
	}
	curve := ecdh.X25519()
	clientPub, err := curve.NewPublicKey(clientPubRaw)
	if err != nil {
		http.Error(w, "invalid pairing public key", 400)
		return
	}
	serverPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	shared, err := serverPriv.ECDH(clientPub)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	p, err := a.security.Trust(strings.TrimSpace(body.DeviceID), strings.TrimSpace(body.Name))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	key := pairingKey(shared, code, strings.TrimSpace(body.DeviceID), a.security.DeviceID())
	ciphertext, nonce, err := sealPairToken(key, p.Token)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"device_id": a.security.DeviceID(), "name": a.Name(), "pubkey": base64.RawURLEncoding.EncodeToString(serverPriv.PublicKey().Bytes()), "nonce": base64.RawURLEncoding.EncodeToString(nonce), "encrypted_token": base64.RawURLEncoding.EncodeToString(ciphertext), "secure_mode": a.SecureMode()})
}

func (a *App) handleBrowserPair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if isLocalRequest(r) {
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	if !a.allowPairAttempt(r) {
		http.Error(w, "too many pairing attempts", 429)
		return
	}
	var body struct{ Code, Name string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if !a.security.CheckPairCode(strings.TrimSpace(body.Code)) {
		http.Error(w, "invalid pairing code", 401)
		return
	}
	id := "browser-" + randomID()
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "Browser"
	}
	p, err := a.security.Trust(id, name)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "lanshare_token", Value: p.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 30 * 24 * 3600})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) handlePairDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !isLocalRequest(r) {
		http.Error(w, "local management only", 403)
		return
	}
	var body struct {
		DeviceID string `json:"device_id"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	code := strings.TrimSpace(body.Code)
	if len(code) != 6 {
		http.Error(w, "pairing code must be 6 digits", 400)
		return
	}
	dev, ok := a.discovery.Find(strings.TrimSpace(body.DeviceID))
	if !ok {
		http.Error(w, "device not found", 404)
		return
	}
	curve := ecdh.X25519()
	clientPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	pub := clientPriv.PublicKey().Bytes()
	proof := pairingProof(code, pub, a.security.DeviceID())
	payload, _ := json.Marshal(map[string]string{"device_id": a.security.DeviceID(), "name": a.Name(), "pubkey": base64.RawURLEncoding.EncodeToString(pub), "proof": base64.RawURLEncoding.EncodeToString(proof)})
	url := fmt.Sprintf("http://%s:%d/api/pair", dev.IP, dev.Port)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.peerClient.Do(req)
	if err != nil {
		http.Error(w, "pair request failed: "+err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		http.Error(w, strings.TrimSpace(string(b)), resp.StatusCode)
		return
	}
	var out struct {
		DeviceID       string `json:"device_id"`
		Name           string `json:"name"`
		PubKey         string `json:"pubkey"`
		Nonce          string `json:"nonce"`
		EncryptedToken string `json:"encrypted_token"`
		SecureMode     bool   `json:"secure_mode"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		http.Error(w, "invalid pair response", 502)
		return
	}
	serverPubRaw, err := base64.RawURLEncoding.DecodeString(out.PubKey)
	if err != nil {
		http.Error(w, "invalid pair response key", 502)
		return
	}
	serverPub, err := curve.NewPublicKey(serverPubRaw)
	if err != nil {
		http.Error(w, "invalid pair response key", 502)
		return
	}
	shared, err := clientPriv.ECDH(serverPub)
	if err != nil {
		http.Error(w, "pair key exchange failed", 502)
		return
	}
	key := pairingKey(shared, code, a.security.DeviceID(), out.DeviceID)
	nonce, err := base64.RawURLEncoding.DecodeString(out.Nonce)
	if err != nil {
		http.Error(w, "invalid pair nonce", 502)
		return
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(out.EncryptedToken)
	if err != nil {
		http.Error(w, "invalid pair token", 502)
		return
	}
	token, err := openPairToken(key, nonce, ciphertext)
	if err != nil {
		http.Error(w, "pair token authentication failed", 401)
		return
	}
	if out.DeviceID == "" {
		out.DeviceID = dev.ID
	}
	if out.Name == "" {
		out.Name = dev.Name
	}
	if err := a.security.SaveCredential(out.DeviceID, out.Name, token); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "device_id": out.DeviceID, "name": out.Name})
}

func (a *App) handleSecurity(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) {
		http.Error(w, "local management only", 403)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]any{"secure_mode": a.SecureMode(), "pair_code": a.security.PairCode(), "trusted": a.security.Trusted()})
	case http.MethodPut:
		var body struct {
			SecureMode *bool `json:"secure_mode"`
			Regenerate bool  `json:"regenerate_pair_code"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if body.SecureMode != nil {
			if err := a.setSecureMode(*body.SecureMode); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		if body.Regenerate {
			a.security.RegeneratePairCode()
		}
		writeJSON(w, 200, map[string]any{"secure_mode": a.SecureMode(), "pair_code": a.security.PairCode()})
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
	}
}

func (a *App) handleRevokeTrust(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !isLocalRequest(r) {
		http.Error(w, "local management only", 403)
		return
	}
	var body struct {
		ID        string `json:"id"`
		Direction string `json:"direction"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	var err error
	if body.Direction == "outgoing" {
		err = a.security.RevokeCredential(body.ID)
	} else {
		err = a.security.RevokeTrusted(body.ID)
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) {
		http.Error(w, "local management only", 403)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]any{"name": a.Name(), "share_dir": a.ShareDir(), "autostart": platform.AutoStartEnabled(), "secure_mode": a.SecureMode()})
	case http.MethodPut:
		var body struct {
			Name      *string `json:"name"`
			AutoStart *bool   `json:"autostart"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if body.Name != nil {
			if err := a.setName(*body.Name); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		}
		if body.AutoStart != nil {
			exe, err := os.Executable()
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if err := platform.SetAutoStart(*body.AutoStart, exe); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		writeJSON(w, 200, map[string]any{"name": a.Name(), "autostart": platform.AutoStartEnabled()})
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
	}
}
