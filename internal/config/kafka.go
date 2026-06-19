package config

import "path/filepath"

// KafkaProxyConfig describes the Kafka SASL proxy configuration, loaded from
// the kafka_proxy section of proxy.yaml.
type KafkaProxyConfig struct {
	Broker       string `json:"broker" yaml:"broker"`
	SASLUsername string `json:"sasl_username" yaml:"sasl_username"`
	SASLPassword string `json:"sasl_password" yaml:"sasl_password"`
	TLS          bool   `json:"tls" yaml:"tls"`
}

// KafkaPidPath returns the pid file path for the kafka proxy of a container.
func KafkaPidPath(name string) string {
	return filepath.Join(StateDir(), name+".kafka.pid")
}

// KafkaLogPath returns the log file path for the kafka proxy of a container.
func KafkaLogPath(name string) string {
	return filepath.Join(StateDir(), name+".kafka.log")
}
