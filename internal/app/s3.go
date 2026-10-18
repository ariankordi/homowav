package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

/*
 * Request signing follows "Signature Version 4 signing process" from the AWS
 * General Reference and the S3 specifics in "Authenticating Requests: Using
 * the Authorization Header (AWS Signature Version 4)":
 * https://docs.aws.amazon.com/AmazonS3/latest/API/sig-v4-header-based-auth.html
 * The implementation is written from that specification; s3_test.go checks it
 * against the worked examples on that page.
 */

const (
	// awsHostSuffix is the AWS S3 domain.
	awsHostSuffix = "amazonaws.com"
	// signingAlgorithm names SigV4 in the Authorization header.
	signingAlgorithm = "AWS4-HMAC-SHA256"
	// signingService is the service name in the credential scope.
	signingService = "s3"
	// signingTerminator ends the credential scope.
	signingTerminator = "aws4_request"
	// amzDateLayout and scopeDateLayout are the two timestamp forms SigV4 uses.
	amzDateLayout   = "20060102T150405Z"
	scopeDateLayout = "20060102"
	// uploadTimeout bounds one PutObject.
	uploadTimeout = 2 * time.Minute
	// maxErrorBody caps how much of an error reply is read for the message.
	maxErrorBody = 4 << 10
	// unreservedExtra are the non-alphanumeric characters SigV4 leaves unescaped.
	unreservedExtra = "-_.~"
)

// S3 uploads to an S3-compatible bucket.
type S3 struct {
	media  Media
	client *http.Client
	now    func() time.Time
}

// NewS3 returns a MediaStore for media. transport may be nil for the default.
func NewS3(media Media, transport http.RoundTripper) (*S3, error) {
	if !media.Configured() {
		return nil, errors.New("media upload: bucket, region and both keys are required")
	}
	return &S3{media: media, client: &http.Client{Transport: transport, Timeout: uploadTimeout}, now: time.Now}, nil
}

// Put implements MediaStore with one PutObject request.
func (s *S3) Put(ctx context.Context, key, contentType string, data []byte) (string, error) {
	target, err := s.objectURL(key)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target.String(), bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if s.media.ACL != "" {
		req.Header.Set("X-Amz-Acl", s.media.ACL)
	}
	payloadHash := sha256.Sum256(data)
	sign(req, hex.EncodeToString(payloadHash[:]), s.media, s.now().UTC())

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("media upload: upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return "", fmt.Errorf("media upload: upload failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	return s.publicURL(key, target), nil
}

// objectURL is where the object is uploaded to.
func (s *S3) objectURL(key string) (*url.URL, error) {
	endpoint := s.media.Endpoint
	if endpoint == "" {
		endpoint = "https://s3." + s.media.Region + "." + awsHostSuffix
	}
	base, err := url.Parse(endpoint)
	if err != nil || base.Host == "" {
		return nil, fmt.Errorf("media upload: endpoint %q is not a URL", s.media.Endpoint)
	}
	target := &url.URL{Scheme: base.Scheme}
	if s.media.PathStyle {
		target.Host = base.Host
		target.Path = "/" + s.media.Bucket + "/" + key
	} else {
		target.Host = s.media.Bucket + "." + base.Host
		target.Path = "/" + key
	}
	// RawPath pins the exact escaping that gets signed.
	target.RawPath = uriEncode(target.Path, false)
	return target, nil
}

// publicURL is what a recipient is given.
func (s *S3) publicURL(key string, uploaded *url.URL) string {
	escapedKey := uriEncode(key, false)
	switch {
	case s.media.PublicBaseURL != "":
		return strings.TrimRight(s.media.PublicBaseURL, "/") + "/" + escapedKey
	case s.media.Endpoint == "" && !s.media.PathStyle:
		// The region-less AWS name: this exact host form is known to pass the
		// recipient side's allow-list.
		return "https://" + s.media.Bucket + ".s3." + awsHostSuffix + "/" + escapedKey
	default:
		return uploaded.Scheme + "://" + uploaded.Host + uploaded.EscapedPath()
	}
}

// sign adds SigV4 headers to req. Every header already on req is signed,
// plus Host and the two x-amz headers set here.
func sign(req *http.Request, payloadHash string, media Media, at time.Time) {
	amzDate := at.Format(amzDateLayout)
	scopeDate := at.Format(scopeDateLayout)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	names := []string{"host"}
	values := map[string]string{"host": req.URL.Host}
	for name, headerValues := range req.Header {
		lower := strings.ToLower(name)
		names = append(names, lower)
		values[lower] = strings.TrimSpace(strings.Join(headerValues, ","))
	}
	slices.Sort(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name + ":" + values[name] + "\n")
	}
	signedHeaders := strings.Join(names, ";")

	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		canonicalQuery(req.URL.Query()),
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := strings.Join([]string{scopeDate, media.Region, signingService, signingTerminator}, "/")
	requestHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{signingAlgorithm, amzDate, scope, hex.EncodeToString(requestHash[:])}, "\n")

	signingKey := []byte("AWS4" + media.SecretAccessKey)
	for _, part := range []string{scopeDate, media.Region, signingService, signingTerminator} {
		signingKey = hmacSHA256(signingKey, part)
	}
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	req.Header.Set("Authorization", fmt.Sprintf("%s Credential=%s/%s,SignedHeaders=%s,Signature=%s",
		signingAlgorithm, media.AccessKeyID, scope, signedHeaders, signature))
}

// canonicalQuery sorts and encodes query parameters the SigV4 way.
func canonicalQuery(query url.Values) string {
	var pairs []string
	for name, queryValues := range query {
		for _, value := range queryValues {
			pairs = append(pairs, uriEncode(name, true)+"="+uriEncode(value, true))
		}
	}
	slices.Sort(pairs)
	return strings.Join(pairs, "&")
}

// uriEncode percent-encodes everything except unreserved characters, with
// upper-case hex. Slashes are kept for paths and encoded for query parts.
func uriEncode(value string, encodeSlash bool) string {
	var out strings.Builder
	for _, char := range []byte(value) {
		isAlphanumeric := (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')
		switch {
		case isAlphanumeric || strings.IndexByte(unreservedExtra, char) >= 0:
			out.WriteByte(char)
		case char == '/' && !encodeSlash:
			out.WriteByte(char)
		default:
			fmt.Fprintf(&out, "%%%02X", char)
		}
	}
	return out.String()
}

func hmacSHA256(key []byte, message string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return mac.Sum(nil)
}
