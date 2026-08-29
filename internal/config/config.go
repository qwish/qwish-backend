package config

import (
	"log"
	"os"

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
}

func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, reading from environment")
	}

	return &Config{
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
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required environment variable %s is not set", key)
	}
	return v
}
