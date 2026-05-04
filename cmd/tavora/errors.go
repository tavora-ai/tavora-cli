package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	tavora "github.com/tavora-ai/tavora-sdk-go"
)

// wrapError transforms raw SDK/network errors into human-readable messages.
func wrapError(err error) error {
	if err == nil {
		return nil
	}

	// API errors with status codes
	var apiErr *tavora.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401:
			return fmt.Errorf("authentication failed — check your API key is valid")
		case 403:
			return fmt.Errorf("access denied — your API key may lack permission for this operation")
		case 404:
			return fmt.Errorf("not found — %s", apiErr.Message)
		case 409:
			return fmt.Errorf("conflict — %s", apiErr.Message)
		case 429:
			return fmt.Errorf("rate limited — too many requests, try again shortly")
		}
		return err
	}

	// Network errors
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return fmt.Errorf("cannot reach server — is it running? (%v)", netErr.Err)
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if strings.Contains(urlErr.Err.Error(), "connection refused") {
			return fmt.Errorf("connection refused — is the Tavora server running?")
		}
		if strings.Contains(urlErr.Err.Error(), "no such host") {
			return fmt.Errorf("unknown host — check your server URL")
		}
	}

	// DNS errors
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Errorf("DNS lookup failed for %s — check your server URL", dnsErr.Name)
	}

	return err
}
