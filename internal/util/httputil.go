package util

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

func NewHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
		MaxIdleConnsPerHost: 10,
	}

	if proxyURL != "" {
		if strings.HasPrefix(proxyURL, "socks5://") || strings.HasPrefix(proxyURL, "socks5h://") {
			// SOCKS5 proxy
			u, err := url.Parse(proxyURL)
			if err != nil {
				return nil, fmt.Errorf("parse socks5 proxy url: %w", err)
			}
			var auth *proxy.Auth
			if u.User != nil {
				pwd, _ := u.User.Password()
				auth = &proxy.Auth{User: u.User.Username(), Password: pwd}
			}
			dialer, err := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
			if err != nil {
				return nil, fmt.Errorf("create socks5 dialer: %w", err)
			}
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			}
		} else {
			// HTTP/HTTPS proxy
			u, err := url.Parse(proxyURL)
			if err != nil {
				return nil, fmt.Errorf("parse http proxy url: %w", err)
			}
			transport.Proxy = http.ProxyURL(u)
		}
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}, nil
}

// NewNoRedirectClient creates an HTTP client that does NOT follow redirects,
// useful for extracting the redirect URL (e.g., confirmation code).
func NewNoRedirectClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	client, err := NewHTTPClient(proxyURL, timeout)
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client, nil
}
