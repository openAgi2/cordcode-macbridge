package gobridge

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"time"
)

// generateSelfSignedCert 生成自签名 RSA 证书，用于 Tailscale wss:// 连接。
// iOS 端通过 InsecureURLSessionDelegate 接受该证书。
func generateSelfSignedCert(hosts ...string) (*tls.Certificate, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("生成密钥失败: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("生成序列号失败: %w", err)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "CordCode Link",
			Organization: []string{"CordCode Self-Signed"},
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost", "*.ts.net"},
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if host != "" {
			tmpl.DNSNames = append(tmpl.DNSNames, host)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("创建证书失败: %w", err)
	}

	cert := &tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  priv,
	}
	return cert, nil
}

// startTLSServer 在指定端口启动 TLS WebSocket 服务器。
// 使用自签名证书，供 Tailscale 远程配对和连接使用。
func startTLSServer(handler http.Handler, port int, cert *tls.Certificate) *http.Server {
	addr := fmt.Sprintf(":%d", port)
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*cert},
		MinVersion:   tls.VersionTLS12,
		// gorilla/websocket 使用 HTTP/1.1 Upgrade；Go 的 TLS server 默认会通过
		// ALPN 启用 HTTP/2，导致 /pairing 走不到普通 WebSocket upgrade 路径。
		NextProtos: []string{"http/1.1"},
	}

	tlsServer := &http.Server{
		Addr:         addr,
		TLSConfig:    tlsConfig,
		Handler:      handler,
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
	}

	go func() {
		slog.Info("go-bridge: TLS listener started", "addr", addr)
		if err := tlsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			slog.Error("go-bridge: TLS server error", "error", err)
		}
	}()

	return tlsServer
}
