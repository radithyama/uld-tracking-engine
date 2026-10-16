// Package config reads service configuration from environment variables.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

func String(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func Int(key string, def int) int {
	if v, err := strconv.Atoi(String(key, "")); err == nil {
		return v
	}
	return def
}

func Duration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(String(key, "")); err == nil {
		return v
	}
	return def
}

// List splits a comma-separated variable, dropping empty entries.
func List(key, def string) []string {
	var out []string
	for _, s := range strings.Split(String(key, def), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Common holds settings shared by every backend service.
type Common struct {
	KafkaBrokers  []string
	KafkaTopic    string
	KafkaDLQTopic string
	KafkaGroupID  string
	DatabaseURL   string
	RedisAddr     string
	LogLevel      string
}

func LoadCommon() Common {
	return Common{
		KafkaBrokers:  List("KAFKA_BROKERS", "localhost:29092"),
		KafkaTopic:    String("KAFKA_TOPIC", "uld.telemetry"),
		KafkaDLQTopic: String("KAFKA_DLQ_TOPIC", "uld.telemetry.dlq"),
		KafkaGroupID:  String("KAFKA_GROUP_ID", "uld-stream-processor"),
		DatabaseURL:   String("DATABASE_URL", "postgres://uld:uld@localhost:5432/uld?sslmode=disable"),
		RedisAddr:     String("REDIS_ADDR", "localhost:6379"),
		LogLevel:      String("LOG_LEVEL", "info"),
	}
}
