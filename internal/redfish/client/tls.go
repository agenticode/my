package client

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// CertError says that the certificate of the BMC was not accepted. It
// carries the fingerprint of the certificate the BMC presented, so that a
// person can compare it with what the BMC itself shows and then name it.
type CertError struct {
	// Reason is why the certificate was not accepted.
	Reason string
	// Fingerprint is the SHA-256 fingerprint of the certificate, in the
	// form "AB:CD:...".
	Fingerprint string
	// Pinned is set when the certificate is not the one that was named by
	// its fingerprint.
	Pinned bool
}

func (e *CertError) Error() string {
	if e.Pinned {
		return "the BMC presents another certificate than the one named by its fingerprint: it presents " + e.Fingerprint
	}
	return "the certificate of the BMC is not trusted: " + e.Reason + ". Its SHA-256 fingerprint is " + e.Fingerprint
}

// ParseFingerprint reads a SHA-256 certificate fingerprint: 64 hexadecimal
// digits, with or without colons, as "openssl x509 -fingerprint -sha256"
// prints it after "=".
func ParseFingerprint(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '='); i >= 0 {
		s = s[i+1:] // "sha256 Fingerprint=AB:CD..."
	}
	s = strings.NewReplacer(":", "", " ", "").Replace(s)
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != sha256.Size {
		return nil, errors.New("a certificate fingerprint is 64 hexadecimal digits (SHA-256), with or without colons")
	}
	return b, nil
}

// FormatFingerprint writes a fingerprint as "AB:CD:...".
func FormatFingerprint(sum []byte) string {
	var b strings.Builder
	for i, x := range sum {
		if i > 0 {
			b.WriteByte(':')
		}
		fmt.Fprintf(&b, "%02X", x)
	}
	return b.String()
}

// tlsConfig builds the TLS settings of a client and names the way the
// certificate is checked.
//
// The certificate is checked here and not by the defaults of crypto/tls,
// for two reasons: a certificate named by its fingerprint has to be
// accepted without a chain, and a certificate that is refused has to be
// reported with its fingerprint. The check itself is the standard one: the
// chain must lead to a trusted certificate and the certificate must be
// valid for the host that was asked for.
func tlsConfig(cfg Config, base *url.URL) (*tls.Config, string, error) {
	if base.Scheme == "http" {
		return nil, "none", nil
	}
	set := 0
	for _, on := range []bool{cfg.Insecure, cfg.Fingerprint != "", cfg.CAFile != ""} {
		if on {
			set++
		}
	}
	if set > 1 {
		return nil, "", errors.New("--ca-file, --fingerprint and --insecure are three ways to treat the certificate: give one")
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}
	switch {
	case cfg.Insecure:
		return tc, "insecure", nil
	case cfg.Fingerprint != "":
		want, err := ParseFingerprint(cfg.Fingerprint)
		if err != nil {
			return nil, "", err
		}
		tc.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return &CertError{Reason: "the BMC presented no certificate"}
			}
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			if subtle.ConstantTimeCompare(sum[:], want) != 1 {
				return &CertError{Pinned: true, Fingerprint: FormatFingerprint(sum[:])}
			}
			return nil
		}
		return tc, "fingerprint", nil
	}
	mode := "verified"
	var roots *x509.CertPool // nil: the certificates of the system
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, "", fmt.Errorf("--ca-file: %w", err)
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, "", fmt.Errorf("--ca-file %s: no certificate in PEM form found", cfg.CAFile)
		}
		mode = "ca-file"
	}
	host := base.Hostname()
	tc.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return &CertError{Reason: "the BMC presented no certificate"}
		}
		leaf := cs.PeerCertificates[0]
		opts := x509.VerifyOptions{Roots: roots, DNSName: host, Intermediates: x509.NewCertPool()}
		for _, c := range cs.PeerCertificates[1:] {
			opts.Intermediates.AddCert(c)
		}
		if _, err := leaf.Verify(opts); err != nil {
			sum := sha256.Sum256(leaf.Raw)
			return &CertError{Reason: certReason(err), Fingerprint: FormatFingerprint(sum[:])}
		}
		return nil
	}
	return tc, mode, nil
}

// certReason says in a few words why a certificate was refused.
func certReason(err error) string {
	var unknown x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	switch {
	case errors.As(err, &unknown):
		return "it is signed by itself or by an authority this machine does not know"
	case errors.As(err, &host):
		return "it is not issued for the address that was asked for"
	case errors.As(err, &invalid) && invalid.Reason == x509.Expired:
		return "it has expired or is not valid yet"
	}
	return err.Error()
}
