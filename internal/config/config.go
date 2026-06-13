package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	Port               string
	AppEnv             string
	DatabaseURL        string
	SupabaseURL        string
	SupabaseAnonKey    string
	SupabaseServiceKey string
	SupabaseJWTSecret  string
	R2AccountID        string
	R2AccessKeyID      string
	R2SecretAccessKey  string
	R2BucketName       string
	R2PublicURL        string
	ResendAPIKey       string
	CronSecret         string
	AllowedOrigins     string
	FCMProjectID       string
	FCMCredentialsJSON string
	AppURL             string
	SuperAdminURL      string // super-admin console; invite links redirect here
	InstituteURL       string // institution dashboard; provision-admin invites redirect here
	BrandURL           string // marketing site; institution "apply to join" links point here
}

func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, reading from environment")
	}

	return &Config{
		Port:               getEnv("PORT", "8080"),
		AppEnv:             getEnv("APP_ENV", "development"),
		DatabaseURL:        mustEnv("DATABASE_URL"),
		SupabaseURL:        mustEnv("SUPABASE_URL"),
		SupabaseAnonKey:    mustEnv("SUPABASE_ANON_KEY"),
		SupabaseServiceKey: mustEnv("SUPABASE_SERVICE_ROLE_KEY"),
		SupabaseJWTSecret:  mustEnv("SUPABASE_JWT_SECRET"),
		R2AccountID:        getEnv("R2_ACCOUNT_ID", ""),
		R2AccessKeyID:      getEnv("R2_ACCESS_KEY_ID", ""),
		R2SecretAccessKey:  getEnv("R2_SECRET_ACCESS_KEY", ""),
		R2BucketName:       getEnv("R2_BUCKET_NAME", "quizapp-media"),
		R2PublicURL:        getEnv("R2_PUBLIC_URL", ""),
		ResendAPIKey:       getEnv("RESEND_API_KEY", ""),
		CronSecret:         getEnv("CRON_SECRET", ""),
		AllowedOrigins:     getEnv("ALLOWED_ORIGINS", "*"),
		FCMProjectID:       getEnv("FCM_PROJECT_ID", ""),
		FCMCredentialsJSON: getEnv("FCM_SERVICE_ACCOUNT_JSON", ""),
		AppURL:             getEnv("APP_URL", "https://app.qwish.in"),
		SuperAdminURL:      getEnv("SUPER_ADMIN_URL", ""),
		InstituteURL:       getEnv("INSTITUTE_DASHBOARD_URL", ""),
		BrandURL:           getEnv("BRAND_URL", "https://qwish.in"),
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
