package config

import "time"

type Temporal struct {
	HostPort        string        `yaml:"host-port"`
	ActivityTimeout time.Duration `yaml:"activity-timeout"`
	QueueName       string        `yaml:"queue-name"`
	Namespace       string        `yaml:"namespace"`
	WorkerCount     int           `yaml:"worker-count"`
}

type Logger struct {
	StdOut bool   `yaml:"stdout"`
	Level  string `yaml:"level"`
	File   File   `yaml:"file"`
}

type File struct {
	Path string `yaml:"path"`
}

type Database struct {
	Host              string        `yaml:"host"`
	ConnectionTimeout time.Duration `yaml:"connection-timeout"`
	MaxConns          int32         `yaml:"max-conns"`
	MinConns          int32         `yaml:"min-conns"`
	MaxConnLifetime   time.Duration `yaml:"max-conn-lifetime"`
}

type GRPCServer struct {
	Port           int  `yaml:"port"`
	MaxRecvMsgSize int  `yaml:"max_recv_msg_size"`
	UseTLS         bool `yaml:"use_tls"`
	TLS            TLS  `yaml:"tls"`
}

type GRPCClient struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	MaxRecvMsgSize int    `yaml:"max_recv_msg_size"`
	UseTLS         bool   `yaml:"use_tls"`
	TLS            TLS    `yaml:"tls"`
}

type TLS struct {
	CaCertPath     string `yaml:"ca_cert_path"`
	ServerCertPath string `yaml:"server_cert_path"`
	ServerKeyPath  string `yaml:"server_key_path"`
	ClientCertPath string `yaml:"client_cert_path"`
	ClientKeyPath  string `yaml:"client_key_path"`
}

type Redis struct {
	Mode              string        `yaml:"mode"`
	Addresses         []string      `yaml:"addresses"`
	Username          string        `yaml:"username"`
	Password          string        `yaml:"password"`
	DB                int           `yaml:"db"`
	ConnectionTimeout time.Duration `yaml:"connection-timeout"`
	ReadTimeout       time.Duration `yaml:"read-timeout"`
	WriteTimeout      time.Duration `yaml:"write-timeout"`
}
