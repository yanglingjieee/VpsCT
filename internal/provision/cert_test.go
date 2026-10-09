package provision

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"net"
	"testing"
)

// The issued certificate is trusted by being handed over: a peer that holds
// it accepts this server under the issued name and nothing else.
func TestIssuedCertificateIsItsOwnTrustAnchor(t *testing.T) {
	for _, name := range []string{"landing.example.com", "203.0.113.77"} {
		cert, key, err := IssueCertificate(name)
		if err != nil {
			t.Fatal(err)
		}
		der, _ := base64.StdEncoding.DecodeString(cert)
		keyDER, _ := base64.StdEncoding.DecodeString(key)
		priv, err := x509.ParseECPrivateKey(keyDER)
		if err != nil {
			t.Fatal(err)
		}
		pair := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
		ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}})
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func() { _ = c.(*tls.Conn).Handshake(); c.Close() }()
			}
		}()
		dial := func(trusted, serverName string) error {
			pool := x509.NewCertPool()
			der, _ := base64.StdEncoding.DecodeString(trusted)
			c, err := x509.ParseCertificate(der)
			if err != nil {
				return err
			}
			pool.AddCert(c)
			conn, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				return err
			}
			defer conn.Close()
			return tls.Client(conn, &tls.Config{RootCAs: pool, ServerName: serverName}).Handshake()
		}
		if err := dial(cert, name); err != nil {
			t.Fatalf("%s: the holder of the certificate must accept it: %v", name, err)
		}
		if dial(cert, "other.example.com") == nil {
			t.Fatalf("%s: accepted under another name", name)
		}
		other, _, _ := IssueCertificate(name)
		if dial(other, name) == nil {
			t.Fatalf("%s: accepted by the holder of a different certificate", name)
		}
		ln.Close()
	}
}
