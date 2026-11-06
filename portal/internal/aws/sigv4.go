// Package aws provides AWS SigV4 signing and Bedrock API client for Portal.
// This allows Portal to directly validate Bedrock Runtime keys without
// depending on the proxy service.
package aws

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// SignRequest signs an HTTP request using AWS Signature Version 4.
// Parameters:
//   - req: the HTTP request to sign (headers will be modified in place)
//   - body: the request body bytes (used for payload hash)
//   - accessKey: AWS access key ID
//   - secretKey: AWS secret access key
//   - sessionToken: AWS session token (empty string if not using temporary credentials)
//   - region: AWS region (e.g. "us-east-1")
//   - service: AWS service name (e.g. "bedrock")
func SignRequest(req *http.Request, body []byte, accessKey, secretKey, sessionToken, region, service string) {
	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	// 1. Create canonical request
	payloadHash := sha256Hex(body)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if sessionToken != "" {
		req.Header.Set("x-amz-security-token", sessionToken)
	}

	canonicalHeaders, signedHeaders := buildCanonicalHeaders(req)

	// AWS SigV4 canonicalisation requires ':' in the path to be
	// percent-encoded as %3A, even though ':' is a valid URL path
	// character per RFC 3986.
	canonicalPath := strings.ReplaceAll(req.URL.EscapedPath(), ":", "%3A")
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalPath,
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	// 2. Create string to sign
	credentialScope := dateStamp + "/" + region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	// 3. Calculate signature
	signingKey := getSignatureKey(secretKey, dateStamp, region, service)
	signature := hmacHex(signingKey, []byte(stringToSign))

	// 4. Build Authorization header
	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature)
	req.Header.Set("Authorization", authHeader)
}

func buildCanonicalHeaders(req *http.Request) (canonical, signed string) {
	headers := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	if req.Header.Get("Content-Type") != "" {
		headers = append([]string{"content-type"}, headers...)
	}
	if req.Header.Get("x-amz-security-token") != "" {
		headers = append(headers, "x-amz-security-token")
	}

	var cb strings.Builder
	for _, h := range headers {
		v := strings.TrimSpace(req.Header.Get(h))
		if v == "" && h == "host" {
			v = req.URL.Host
		}
		cb.WriteString(strings.ToLower(h))
		cb.WriteString(":")
		cb.WriteString(v)
		cb.WriteString("\n")
	}

	return cb.String(), strings.Join(headers, ";")
}

func getSignatureKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return kSigning
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hmacHex(key, data []byte) string {
	return hex.EncodeToString(hmacSHA256(key, data))
}

func sha256Hex(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
