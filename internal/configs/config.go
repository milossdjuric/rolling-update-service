package configs

import "os"

type Config struct {
	natsAddress           string
	etcdAddress           string
	serverAddress         string
	magnetarAddress       string
	dockerAddress         string
	rateLimiterMaxTokens  int
	rateLimiterRefillRate int
}

func (c *Config) NatsAddress() string {
	return c.natsAddress
}

func (c *Config) EtcdAddress() string {
	return c.etcdAddress
}

func (c *Config) ServerAddress() string {
	return c.serverAddress
}

func (c *Config) MagnetarAddress() string {
	return c.magnetarAddress
}

func (c *Config) DockerAddress() string {
	return c.dockerAddress
}

func (c *Config) RateLimiterMaxTokens() int {
	return c.rateLimiterMaxTokens
}

func (c *Config) RateLimiterRefillRate() int {
	return c.rateLimiterRefillRate
}

func NewFromEnv() (*Config, error) {
	return &Config{
		natsAddress:     os.Getenv("NATS_ADDRESS"),
		etcdAddress:     os.Getenv("UPDATE_SERVICE_ETCD_ADDRESS"),
		serverAddress:   os.Getenv("UPDATE_SERVICE_ADDRESS"),
		magnetarAddress: os.Getenv("MAGNETAR_ADDRESS"),
		dockerAddress:   os.Getenv("UPDATE_SERVICE_DOCKER_CLIENT_ADDRESS"),
	}, nil
}
