package config

import (
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                     string
	AppEnv                   string
	DatabaseURL              string
	SupabaseURL              string
	SupabaseAnonKey          string
	SupabaseServiceKey       string
	SupabaseJWTSecret        string
	R2AccountID              string
	R2AccessKeyID            string
	R2SecretAccessKey        string
	R2BucketName             string
	R2PublicURL              string
	ResendAPIKey             string
	CronSecret               string
	AllowedOrigins           string
	FCMProjectID             string
	FCMCredentialsJSON       string
	PlayIntegrityMode        string // off, observe, enforce
	PlayIntegrityCredentials string // service account JSON from linked Cloud project
	AppURL                   string
	SuperAdminURL            string // super-admin console; invite links redirect here
	InstituteURL             string // institution dashboard; provision-admin invites redirect here
	TeacherURL               string // teacher panel; teacher-verified emails link here to sign in
	BrandURL                 string // marketing site; institution "apply to join" links point here
	WebAuthnRPID             string // passkey Relying Party ID (registrable domain, no scheme/port)
	WebAuthnRPDisplayName    string // passkey RP display name shown by the authenticator
	WebAuthnRPOrigins        string // comma-separated list of allowed passkey origins (with scheme)
	TurnstileSecret          string // Cloudflare Turnstile secret; empty disables bot verification on public forms
	RecruiterTestLoginSecret string // non-production only; enables recruiter test login
	RecruiterTestLoginEmail  string // active recruiter membership used by test login
	DemoLoginEmail           string // store-review account; empty disables the fixed-OTP login
	DemoLoginOTP             string // the 6-digit code that address accepts
}

func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, reading from environment")
	}

	cfg := &Config{
		Port:                     getEnv("PORT", "8080"),
		AppEnv:                   getEnv("APP_ENV", "development"),
		DatabaseURL:              mustEnv("DATABASE_URL"),
		SupabaseURL:              mustEnv("SUPABASE_URL"),
		SupabaseAnonKey:          mustEnv("SUPABASE_ANON_KEY"),
		SupabaseServiceKey:       mustEnv("SUPABASE_SERVICE_ROLE_KEY"),
		SupabaseJWTSecret:        mustEnv("SUPABASE_JWT_SECRET"),
		R2AccountID:              getEnv("R2_ACCOUNT_ID", ""),
		R2AccessKeyID:            getEnv("R2_ACCESS_KEY_ID", ""),
		R2SecretAccessKey:        getEnv("R2_SECRET_ACCESS_KEY", ""),
		R2BucketName:             getEnv("R2_BUCKET_NAME", "quizapp-media"),
		R2PublicURL:              getEnv("R2_PUBLIC_URL", ""),
		ResendAPIKey:             getEnv("RESEND_API_KEY", ""),
		CronSecret:               getEnv("CRON_SECRET", ""),
		AllowedOrigins:           getEnv("ALLOWED_ORIGINS", "*"),
		FCMProjectID:             getEnv("FCM_PROJECT_ID", ""),
		FCMCredentialsJSON:       getEnv("FCM_SERVICE_ACCOUNT_JSON", ""),
		PlayIntegrityMode:        getEnv("PLAY_INTEGRITY_MODE", "off"),
		PlayIntegrityCredentials: getEnv("PLAY_INTEGRITY_SERVICE_ACCOUNT_JSON", ""),
		AppURL:                   getEnv("APP_URL", "https://app.qwish.in"),
		SuperAdminURL:            getEnv("SUPER_ADMIN_URL", "https://superadmin.qwish.in"),
		InstituteURL:             getEnv("INSTITUTE_DASHBOARD_URL", "https://institute.qwish.in"),
		TeacherURL:               getEnv("TEACHER_PANEL_URL", "https://teacher.qwish.in"),
		BrandURL:                 getEnv("BRAND_URL", "https://qwish.in"),
		WebAuthnRPID:             getEnv("WEBAUTHN_RP_ID", "localhost"),
		WebAuthnRPDisplayName:    getEnv("WEBAUTHN_RP_DISPLAY_NAME", "Qwish Admin"),
		WebAuthnRPOrigins:        getEnv("WEBAUTHN_RP_ORIGINS", "https://superadmin.qwish.in"),
		TurnstileSecret:          getEnv("TURNSTILE_SECRET", ""),
		RecruiterTestLoginSecret: getEnv("RECRUITER_TEST_LOGIN_SECRET", ""),
		RecruiterTestLoginEmail:  getEnv("RECRUITER_TEST_LOGIN_EMAIL", ""),
		DemoLoginEmail:           strings.ToLower(strings.TrimSpace(getEnv("DEMO_LOGIN_EMAIL", ""))),
		DemoLoginOTP:             getEnv("DEMO_LOGIN_OTP", ""),
	}
	// Unlike the recruiter test login this one is allowed in production — app
	// store review runs against prod. Half a configuration is a foot-gun, so
	// refuse to boot on one: an address with no code would silently fall
	// through to the real OTP path and fail review with no signal.
	if (cfg.DemoLoginEmail == "") != (cfg.DemoLoginOTP == "") {
		log.Fatal("DEMO_LOGIN_EMAIL and DEMO_LOGIN_OTP must be set together")
	}
	// The app's OTP field is exactly six digits and digits-only, so a code it
	// cannot type is a code no reviewer can use.
	if cfg.DemoLoginOTP != "" && !sixDigits(cfg.DemoLoginOTP) {
		log.Fatal("DEMO_LOGIN_OTP must be exactly 6 digits")
	}
	if cfg.AppEnv == "production" {
		for key, value := range map[string]string{
			"ALLOWED_ORIGINS":          cfg.AllowedOrigins,
			"CRON_SECRET":              cfg.CronSecret,
			"FCM_PROJECT_ID":           cfg.FCMProjectID,
			"FCM_SERVICE_ACCOUNT_JSON": cfg.FCMCredentialsJSON,
		} {
			if value == "" || (key == "ALLOWED_ORIGINS" && value == "*") {
				log.Fatalf("production requires a non-wildcard %s", key)
			}
		}
	}
	return cfg
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func sixDigits(v string) bool {
	if len(v) != 6 {
		return false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required environment variable %s is not set", key)
	}
	return v
}
