package mediastore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hwclient/config"
)

// The example credentials and expected signatures below are the published
// worked examples from the AWS S3 SigV4 documentation:
// https://docs.aws.amazon.com/AmazonS3/latest/API/sig-v4-header-based-auth.html
var docMedia = config.Media{
	Region:          "us-east-1",
	Bucket:          "examplebucket",
	AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
	SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
}

var docTime = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func TestSignMatchesAWSGetObjectExample(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	sign(req, emptyPayloadHash, docMedia, docTime)
	const want = "Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); !strings.HasSuffix(got, want) {
		t.Errorf("Authorization = %q, want suffix %q", got, want)
	}
}

func TestSignMatchesAWSPutObjectExample(t *testing.T) {
	body := "Welcome to Amazon S3."
	hash := sha256.Sum256([]byte(body))
	req, _ := http.NewRequest(http.MethodPut, "https://examplebucket.s3.amazonaws.com/", strings.NewReader(body))
	// "$" must be signed as %24: the example exists to test path escaping.
	req.URL.Path, req.URL.RawPath = "/test$file.text", "/test%24file.text"
	req.Header.Set("Date", "Fri, 24 May 2013 00:00:00 GMT")
	req.Header.Set("X-Amz-Storage-Class", "REDUCED_REDUNDANCY")
	sign(req, hex.EncodeToString(hash[:]), docMedia, docTime)
	const want = "Signature=98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd"
	if got := req.Header.Get("Authorization"); !strings.HasSuffix(got, want) {
		t.Errorf("Authorization = %q, want suffix %q", got, want)
	}
}

func TestPutUploadsAndReturnsPublicURL(t *testing.T) {
	var gotPath, gotType, gotACL, gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		gotPath, gotType, gotACL, gotAuth, gotBody = r.URL.EscapedPath(), r.Header.Get("Content-Type"), r.Header.Get("X-Amz-Acl"), r.Header.Get("Authorization"), string(data)
	}))
	defer server.Close()

	media := docMedia
	media.Endpoint, media.PathStyle, media.ACL, media.PublicBaseURL = server.URL, true, "public-read", "https://cdn.example.com/media/"
	store, err := NewS3(media, nil)
	if err != nil {
		t.Fatal(err)
	}
	publicURL, err := store.Put(context.Background(), "hw/a b.gif", "image/gif", []byte("GIF89a"))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/examplebucket/hw/a%20b.gif" || gotType != "image/gif" || gotACL != "public-read" || gotBody != "GIF89a" {
		t.Errorf("upload: path %q type %q acl %q body %q", gotPath, gotType, gotACL, gotBody)
	}
	if !strings.Contains(gotAuth, "SignedHeaders=content-type;host;x-amz-acl;x-amz-content-sha256;x-amz-date") {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if publicURL != "https://cdn.example.com/media/hw/a%20b.gif" {
		t.Errorf("public URL = %q", publicURL)
	}
}

func TestPutReportsProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "<Error><Code>AccessDenied</Code></Error>", http.StatusForbidden)
	}))
	defer server.Close()
	media := docMedia
	media.Endpoint, media.PathStyle = server.URL, true
	store, _ := NewS3(media, nil)
	if _, err := store.Put(context.Background(), "k", "", nil); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("err = %v, want the provider's message", err)
	}
}

func TestPublicURLDefaults(t *testing.T) {
	aws, _ := NewS3(docMedia, nil)
	target, err := aws.objectURL("hw/x.png")
	if err != nil || target.String() != "https://examplebucket.s3.us-east-1.amazonaws.com/hw/x.png" {
		t.Errorf("AWS upload URL = %v, %v", target, err)
	}
	if got := aws.publicURL("hw/x.png", target); got != "https://examplebucket.s3.amazonaws.com/hw/x.png" {
		t.Errorf("AWS public URL = %q", got)
	}
	if _, err := NewS3(config.Media{Bucket: "b"}, nil); err == nil {
		t.Error("NewS3 accepted an incomplete configuration")
	}
}

func TestNewKey(t *testing.T) {
	key, err := NewKey("hw/", "Holiday Photo (1).JPEG")
	if err != nil || !strings.HasPrefix(key, "hw/") || !strings.HasSuffix(key, ".jpeg") || strings.Contains(key, "Holiday") {
		t.Errorf("key = %q, %v", key, err)
	}
	if other, _ := NewKey("hw/", "Holiday Photo (1).JPEG"); other == key {
		t.Error("two keys for the same name collided")
	}
	if key, _ := NewKey("", "evil.p/../x"); strings.ContainsAny(key, "/.") {
		t.Errorf("unsafe extension survived: %q", key)
	}
}
