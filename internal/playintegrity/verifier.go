package playintegrity

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/qwish/backend/internal/httpx"
)

const (
	packageName = "com.qwish.numpie"
	scope       = "https://www.googleapis.com/auth/playintegrity"
	tokenURL    = "https://oauth2.googleapis.com/token"
)

// Mode is deliberately off until the Play Console and Android release are ready.
// Observe verifies and logs verdicts without changing quiz completion behavior.
type Mode string

const (
	Off     Mode = "off"
	Observe Mode = "observe"
	Enforce Mode = "enforce"
)

type Verifier struct {
	mode        Mode
	email       string
	key         *rsa.PrivateKey
	mu          sync.Mutex
	accessToken string
	expires     time.Time
}

type serviceAccount struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

type verdict struct {
	TokenPayloadExternal struct {
		RequestDetails struct {
			RequestPackageName string `json:"requestPackageName"`
			RequestHash        string `json:"requestHash"`
			TimestampMillis    int64  `json:"timestampMillis"`
		} `json:"requestDetails"`
		AccountDetails struct {
			AppLicensingVerdict string `json:"appLicensingVerdict"`
		} `json:"accountDetails"`
		AppIntegrity struct {
			AppRecognitionVerdict string `json:"appRecognitionVerdict"`
			PackageName           string `json:"packageName"`
		} `json:"appIntegrity"`
		DeviceIntegrity struct {
			DeviceRecognitionVerdict []string `json:"deviceRecognitionVerdict"`
			DeviceAttributes         struct {
				SDKVersion int `json:"sdkVersion"`
			} `json:"deviceAttributes"`
			RecentDeviceActivity struct {
				DeviceActivityLevel string `json:"deviceActivityLevel"`
			} `json:"recentDeviceActivity"`
		} `json:"deviceIntegrity"`
		EnvironmentDetails struct {
			PlayProtectVerdict   string `json:"playProtectVerdict"`
			AppAccessRiskVerdict struct {
				AppsDetected []string `json:"appsDetected"`
			} `json:"appAccessRiskVerdict"`
		} `json:"environmentDetails"`
	} `json:"tokenPayloadExternal"`
}

type Result struct {
	Trusted     bool
	App         string
	License     string
	Device      []string
	SDKVersion  int
	Activity    string
	PlayProtect string
	AppAccess   []string
	RiskFlags   []string
}

func New(modeText, credentialsJSON string) (*Verifier, error) {
	mode := Mode(strings.ToLower(strings.TrimSpace(modeText)))
	if mode == "" {
		mode = Off
	}
	if mode != Off && mode != Observe && mode != Enforce {
		return nil, fmt.Errorf("invalid PLAY_INTEGRITY_MODE %q", modeText)
	}
	v := &Verifier{mode: mode}
	if mode == Off {
		return v, nil
	}
	var account serviceAccount
	if err := json.Unmarshal([]byte(credentialsJSON), &account); err != nil {
		return nil, fmt.Errorf("parse PLAY_INTEGRITY_SERVICE_ACCOUNT_JSON: %w", err)
	}
	if account.ClientEmail == "" || account.PrivateKey == "" {
		return nil, errors.New("PLAY_INTEGRITY_SERVICE_ACCOUNT_JSON needs client_email and private_key")
	}
	block, _ := pem.Decode([]byte(account.PrivateKey))
	if block == nil {
		return nil, errors.New("PLAY_INTEGRITY_SERVICE_ACCOUNT_JSON private_key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse Play Integrity private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("Play Integrity private key is not RSA")
	}
	v.email, v.key = account.ClientEmail, key
	return v, nil
}

func (v *Verifier) Mode() Mode { return v.mode }

// CompletionHash must match the hash made in MainActivity. The attempt ID is
// unique and the completion handler separately checks its authenticated owner.
func CompletionHash(attemptID string) string {
	sum := sha256.Sum256([]byte("quiz_complete:v1:" + attemptID))
	return hex.EncodeToString(sum[:])
}

func (v *Verifier) Verify(ctx context.Context, attemptID, integrityToken string) (Result, error) {
	if strings.TrimSpace(integrityToken) == "" {
		return Result{}, errors.New("missing integrity token")
	}
	access, err := v.oauthToken(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("Play Integrity authentication: %w", err)
	}
	body, _ := json.Marshal(map[string]string{"integrity_token": integrityToken})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://playintegrity.googleapis.com/v1/"+packageName+":decodeIntegrityToken", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpx.Client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("decode integrity token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Google's response may contain request metadata; never log the token.
		return Result{}, fmt.Errorf("decode integrity token: HTTP %d", resp.StatusCode)
	}
	var payload verdict
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(&payload); err != nil {
		return Result{}, fmt.Errorf("decode integrity response: %w", err)
	}
	p := payload.TokenPayloadExternal
	if p.RequestDetails.RequestPackageName != packageName || p.RequestDetails.RequestHash != CompletionHash(attemptID) {
		return Result{}, errors.New("integrity token does not match the completion request")
	}
	age := time.Since(time.UnixMilli(p.RequestDetails.TimestampMillis))
	if p.RequestDetails.TimestampMillis == 0 || age < -30*time.Second || age > 2*time.Minute {
		return Result{}, errors.New("integrity token is stale")
	}
	result := Result{
		App:         p.AppIntegrity.AppRecognitionVerdict,
		License:     p.AccountDetails.AppLicensingVerdict,
		Device:      p.DeviceIntegrity.DeviceRecognitionVerdict,
		SDKVersion:  p.DeviceIntegrity.DeviceAttributes.SDKVersion,
		Activity:    p.DeviceIntegrity.RecentDeviceActivity.DeviceActivityLevel,
		PlayProtect: p.EnvironmentDetails.PlayProtectVerdict,
		AppAccess:   p.EnvironmentDetails.AppAccessRiskVerdict.AppsDetected,
	}
	deviceTrusted := false
	for _, label := range result.Device {
		if label == "MEETS_DEVICE_INTEGRITY" {
			deviceTrusted = true
			break
		}
	}
	result.Trusted = p.AppIntegrity.PackageName == packageName && result.App == "PLAY_RECOGNIZED" && result.License == "LICENSED" && deviceTrusted
	if result.Activity == "LEVEL_4" {
		result.RiskFlags = append(result.RiskFlags, "high_device_activity")
	}
	if result.PlayProtect == "MEDIUM_RISK" || result.PlayProtect == "HIGH_RISK" {
		result.RiskFlags = append(result.RiskFlags, "play_protect_risk")
	}
	for _, access := range result.AppAccess {
		if strings.HasSuffix(access, "_CAPTURING") || strings.HasSuffix(access, "_CONTROLLING") {
			result.RiskFlags = append(result.RiskFlags, "app_access_risk")
			break
		}
	}
	return result, nil
}

func (v *Verifier) oauthToken(ctx context.Context) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.accessToken != "" && time.Now().Before(v.expires.Add(-time.Minute)) {
		return v.accessToken, nil
	}
	now := time.Now()
	claims := jwt.MapClaims{"iss": v.email, "scope": scope, "aud": tokenURL, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(v.key)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {signed}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpx.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OAuth token exchange: HTTP %d", resp.StatusCode)
	}
	var token struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(&token); err != nil {
		return "", err
	}
	if token.AccessToken == "" {
		return "", errors.New("OAuth returned no access token")
	}
	v.accessToken, v.expires = token.AccessToken, time.Now().Add(time.Duration(token.ExpiresIn)*time.Second)
	return v.accessToken, nil
}
