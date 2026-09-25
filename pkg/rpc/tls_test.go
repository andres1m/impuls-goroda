package rpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/andres1m/impuls-goroda/pkg/config"
	"go.uber.org/zap"
	"google.golang.org/grpc/health"
	pb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestMutualTLSUsesConfiguredCA(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, kind string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: data}), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	caPath := write("ca.pem", "CERTIFICATE", caDER)
	certPath := write("cert.pem", "CERTIFICATE", der)
	keyPath := write("key.pem", "EC PRIVATE KEY", keyDER)
	tlsCfg := config.TLS{CaCertPath: caPath, ServerCertPath: certPath, ServerKeyPath: keyPath, ClientCertPath: certPath, ClientKeyPath: keyPath}
	s := NewServer("tls", zap.NewNop(), &config.GRPCServer{UseTLS: true, TLS: tlsCfg})
	s.OnInit(func(s *Server) { pb.RegisterHealthServer(s.GetServer(), health.NewServer()) })
	if err := s.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()
	defer func() { s.server.Stop(); <-done }()
	_, portStr, err := net.SplitHostPort(s.lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient("tls", zap.NewNop(), &config.GRPCClient{Host: "127.0.0.1", Port: port, UseTLS: true, TLS: tlsCfg})
	if err := c.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Stop(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.HealthCheck(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pb.NewHealthClient(c.GetConn()).Check(ctx, &pb.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}
}
